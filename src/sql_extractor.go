package src

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"mysqlbinlogo/config"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
)

// SQL 이벤트 추출기
type SQLExtractor struct {
	config config.Config
	syncer *replication.BinlogSyncer
}

// 새 SQL 추출기 생성
func NewSQLExtractor(cfg config.Config) *SQLExtractor {
	// 생성 시점에 syncer 초기화
	syncerCfg := replication.BinlogSyncerConfig{
		ServerID: 100,
		Flavor:   "mysql",
		Host:     cfg.Host,
		Port:     uint16(cfg.Port),
		User:     cfg.User,
		Password: cfg.Password,
		Logger:   &config.NullLogger{},
	}

	return &SQLExtractor{
		config: cfg,
		syncer: replication.NewBinlogSyncer(syncerCfg),
	}
}

// 추출기 종료
func (se *SQLExtractor) Close() {
	if se.syncer != nil {
		se.syncer.Close()
		se.syncer = nil
	}
}

// 지정된 파일들에서 SQL 이벤트 추출
func (se *SQLExtractor) ExtractSQLEvents(files []config.BinlogFile) ([]config.SQLEvent, error) {
	var allEvents []config.SQLEvent

	for i, file := range files {
		if se.config.Verbose {
			logrus.Debugf("파일 분석 중: %s (%d/%d)\n", file.Name, i+1, len(files))
		}

		// 하나의 syncer로 각 파일 처리
		events, err := se.ExtractFromSingleFile(file)
		if err != nil {
			if se.config.Verbose {
				logrus.Debugf("파일 %s 분석 실패: %v (계속 진행)\n", file.Name, err)
			}
			continue // 실패한 파일은 건너뛰고 계속
		}

		allEvents = append(allEvents, events...)

		if se.config.Verbose {
			logrus.Debugf("파일 %s에서 %d개 이벤트 추출\n", file.Name, len(events))
		}
	}

	return allEvents, nil
}

// ExtractFromSingleFile 단일 파일에서 SQL 이벤트 추출 (각 파일마다 새로운 syncer 사용)
func (se *SQLExtractor) ExtractFromSingleFile(file config.BinlogFile) ([]config.SQLEvent, error) {
	var events []config.SQLEvent

	// 각 파일마다 새로운 syncer 생성
	cfg := replication.BinlogSyncerConfig{
		ServerID: 100,
		Flavor:   "mysql",
		Host:     se.config.Host,
		Port:     uint16(se.config.Port),
		User:     se.config.User,
		Password: se.config.Password,
		Logger:   &config.NullLogger{},
	}
	syncer := replication.NewBinlogSyncer(cfg)

	// 안전한 syncer 종료를 위한 함수
	var syncerClosed bool
	safeSyncerClose := func() {
		if !syncerClosed {
			syncerClosed = true
			// 에러를 무시하고 조용히 닫기
			func() {
				defer func() {
					if r := recover(); r != nil {
						// panic 무시
					}
				}()

				// syncer가 이미 닫혀있는지 확인
				if syncer != nil {
					// Close() 호출 전에 잠시 대기
					time.Sleep(10 * time.Millisecond)
					syncer.Close()
				}
			}()
		}
	}
	defer safeSyncerClose()

	// Binary log 스트리밍 시작
	streamer, err := syncer.StartSync(mysql.Position{Name: file.Name, Pos: 4})
	if err != nil {
		return nil, fmt.Errorf("파일 %s 스트리밍 시작 실패: %v", file.Name, err)
	}

	// 타임아웃 설정 (30초로 단축)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	eventCount := 0
	maxEvents := 10000 // 10000개 → 5000개로 단축 (더 빠른 처리)
	totalEvents := 0   // 전체 이벤트 카운트 (디버깅용)

	for eventCount < maxEvents {
		select {
		case <-ctx.Done():
			if se.config.Verbose {
				fmt.Printf("파일 %s 처리 시간 초과 (60초)\n", file.Name)
			}
			// 타임아웃 시 안전하게 종료
			safeSyncerClose()
			return events, nil
		default:
			// 논블로킹으로 이벤트 가져오기 시도
			ev, err := func() (*replication.BinlogEvent, error) {
				defer func() {
					if r := recover(); r != nil {
						// panic을 에러로 변환
						err = fmt.Errorf("syncer panic: %v", r)
					}
				}()
				return streamer.GetEvent(ctx)
			}()

			if err != nil {
				// 에러 발생 시 조용히 종료
				if se.config.Verbose {
					fmt.Printf("파일 %s: 이벤트 읽기 완료 (총 %d개 이벤트 처리, 조건 맞는 %d개)\n",
						file.Name, totalEvents, len(events))
				}
				safeSyncerClose()
				return events, nil
			}

			totalEvents++

			// 시간 필터링
			eventTime := time.Unix(int64(ev.Header.Timestamp), 0)

			// 시작 시간 이전이면 스킵
			if eventTime.Before(se.config.StartTime) {
				continue
			}
			// 종료 시간 이후면 해당 파일 처리 완료
			if eventTime.After(se.config.EndTime) {
				if se.config.Verbose {
					fmt.Printf("\n> 파일 %s: 종료 시간 초과 (총 %d개 이벤트 처리, 조건 맞는 %d개)\n",
						file.Name, totalEvents, len(events))
				}
				safeSyncerClose()
				return events, nil
			}

			// SQL 이벤트로 변환
			sqlEvent := se.convertToSQLEvent(ev, file.Name)
			if sqlEvent != nil {
				events = append(events, *sqlEvent)
			}

			// 실제 처리된 이벤트만 카운트
			eventCount++
		}
	}

	if se.config.Verbose {
		fmt.Printf("파일 %s: 최대 이벤트 수(%d) 도달 (총 %d개 이벤트 처리, 조건 맞는 %d개)\n",
			file.Name, maxEvents, totalEvents, len(events))
	}

	safeSyncerClose()
	return events, nil
}

// BinlogEvent를 SQLEvent로 변환
func (se *SQLExtractor) convertToSQLEvent(ev *replication.BinlogEvent, filename string) *config.SQLEvent {
	timestamp := time.Unix(int64(ev.Header.Timestamp), 0)

	switch e := ev.Event.(type) {
	case *replication.QueryEvent:
		query := string(e.Query)
		// 시스템 쿼리나 의미없는 쿼리 필터링
		if se.skipQuery(query) {
			return nil
		}

		return &config.SQLEvent{
			Timestamp: timestamp,
			EventType: "QUERY",
			Database:  string(e.Schema),
			SQL:       query,
			ServerId:  ev.Header.ServerID,
			Position:  ev.Header.LogPos,
			Filename:  filename,
		}

	case *replication.RowsEvent:
		// Row 이벤트 처리
		return se.handleRowsEvent(ev, e, timestamp, filename)

	default:
		// 기타 이벤트는 무시
		return nil
	}
}

// Row 이벤트를 SQLEvent로 변환
func (se *SQLExtractor) handleRowsEvent(ev *replication.BinlogEvent, rowsEvent *replication.RowsEvent, timestamp time.Time, filename string) *config.SQLEvent {
	var eventType string
	var sql string

	switch ev.Header.EventType {
	case replication.WRITE_ROWS_EVENTv1, replication.WRITE_ROWS_EVENTv2:
		eventType = "INSERT"
		sql = se.formatInsertEvent(rowsEvent)
	case replication.UPDATE_ROWS_EVENTv1, replication.UPDATE_ROWS_EVENTv2:
		eventType = "UPDATE"
		sql = se.formatUpdateEvent(rowsEvent)
	case replication.DELETE_ROWS_EVENTv1, replication.DELETE_ROWS_EVENTv2:
		eventType = "DELETE"
		sql = se.formatDeleteEvent(rowsEvent)
	default:
		return nil
	}

	return &config.SQLEvent{
		Timestamp: timestamp,
		EventType: eventType,
		Database:  string(rowsEvent.Table.Schema),
		SQL:       sql,
		ServerId:  ev.Header.ServerID,
		Position:  ev.Header.LogPos,
		Filename:  filename,
	}
}

// 스킵해야 할 쿼리인지 확인
func (se *SQLExtractor) skipQuery(query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))

	// 빈 쿼리
	if query == "" {
		return true
	}

	// 시스템 쿼리들
	skipPrefixes := []string{
		"begin",
		"commit",
		"rollback",
		"set timestamp",
		"set autocommit",
		"# at ",
		"#",
		"/*!",
	}

	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(query, prefix) {
			return true
		}
	}

	return false
}

// INSERT 이벤트를 SQL로 포맷
func (se *SQLExtractor) formatInsertEvent(rowsEvent *replication.RowsEvent) string {
	tableName := string(rowsEvent.Table.Table)
	schema := string(rowsEvent.Table.Schema)
	rowCount := len(rowsEvent.Rows)

	if schema != "" {
		tableName = fmt.Sprintf("%s.%s", schema, tableName)
	}

	// 첫 번째 행의 값들을 보여주기
	var valueStr string
	if rowCount > 0 && len(rowsEvent.Rows[0]) > 0 {
		values := make([]string, len(rowsEvent.Rows[0]))
		for i, val := range rowsEvent.Rows[0] {
			values[i] = se.formatValue(val)
		}
		valueStr = fmt.Sprintf("(%s)", strings.Join(values, ", "))

		if rowCount > 1 {
			valueStr += fmt.Sprintf(" /* and %d more rows */", rowCount-1)
		}
	} else {
		valueStr = "(...)"
	}

	return fmt.Sprintf("INSERT INTO %s VALUES %s", tableName, valueStr)
}

// UPDATE 이벤트를 SQL로 포맷
func (se *SQLExtractor) formatUpdateEvent(rowsEvent *replication.RowsEvent) string {
	tableName := string(rowsEvent.Table.Table)
	schema := string(rowsEvent.Table.Schema)
	rowCount := len(rowsEvent.Rows) / 2 // UPDATE는 before/after 쌍

	if schema != "" {
		tableName = fmt.Sprintf("%s.%s", schema, tableName)
	}

	// 첫 번째 업데이트의 before/after 값 보여주기
	var updateInfo string
	if rowCount > 0 && len(rowsEvent.Rows) >= 2 {
		beforeRow := rowsEvent.Rows[0]
		afterRow := rowsEvent.Rows[1]

		// 변경된 컬럼들만 찾기
		var changes []string
		for i := 0; i < len(beforeRow) && i < len(afterRow); i++ {
			if !se.valuesEqual(beforeRow[i], afterRow[i]) {
				changes = append(changes, fmt.Sprintf("col_%d=%s (was %s)",
					i+1, se.formatValue(afterRow[i]), se.formatValue(beforeRow[i])))
			}
		}

		if len(changes) > 0 {
			updateInfo = strings.Join(changes, ", ")
		} else {
			updateInfo = "/* no visible changes */"
		}

		if rowCount > 1 {
			updateInfo += fmt.Sprintf(" /* and %d more rows */", rowCount-1)
		}
	} else {
		updateInfo = "..."
	}

	return fmt.Sprintf("UPDATE %s SET %s", tableName, updateInfo)
}

// DELETE 이벤트를 SQL로 포맷
func (se *SQLExtractor) formatDeleteEvent(rowsEvent *replication.RowsEvent) string {
	tableName := string(rowsEvent.Table.Table)
	schema := string(rowsEvent.Table.Schema)
	rowCount := len(rowsEvent.Rows)

	if schema != "" {
		tableName = fmt.Sprintf("%s.%s", schema, tableName)
	}

	// 첫 번째 삭제된 행의 값들 보여주기
	var whereClause string
	if rowCount > 0 && len(rowsEvent.Rows[0]) > 0 {
		conditions := make([]string, 0, len(rowsEvent.Rows[0]))
		for i, val := range rowsEvent.Rows[0] {
			if val != nil { // NULL이 아닌 값들만 WHERE 조건으로 사용
				conditions = append(conditions, fmt.Sprintf("col_%d=%s", i+1, se.formatValue(val)))
			}
		}

		if len(conditions) > 0 {
			whereClause = strings.Join(conditions, " AND ")
			if len(conditions) > 3 {
				whereClause = strings.Join(conditions[:3], " AND ") + " /* ... */"
			}
		} else {
			whereClause = "/* all columns NULL */"
		}

		if rowCount > 1 {
			whereClause += fmt.Sprintf(" /* and %d more rows */", rowCount-1)
		}
	} else {
		whereClause = "..."
	}

	return fmt.Sprintf("DELETE FROM %s WHERE %s", tableName, whereClause)
}
