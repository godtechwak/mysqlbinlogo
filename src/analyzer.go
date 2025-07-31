package src

import (
	"database/sql"
	"fmt"
	"os"
	"sort"

	"github.com/sirupsen/logrus"

	"mysqlbinlogo/config"

	_ "github.com/go-sql-driver/mysql"
)

// BinlogAnalyzer Binary log 분석기
type BinlogAnalyzer struct {
	Config config.Config
	conn   *sql.DB
}

// Analyze Binary log 분석 실행
func (ba *BinlogAnalyzer) Analyze() error {
	if ba.Config.Verbose {
		logrus.Debugf("MySQL 서버에 연결 중... %s:%d\n", ba.Config.Host, ba.Config.Port)
	}

	// MySQL 연결
	if err := ba.connect(); err != nil {
		return fmt.Errorf("MySQL 연결 실패: %v", err)
	}
	defer ba.conn.Close()

	// Binary log 파일 목록 가져오기
	binlogFiles, err := ba.getBinlogFiles()
	if err != nil {
		return fmt.Errorf("binary log 파일 목록 가져오기 실패: %v", err)
	}

	if ba.Config.Verbose {
		logrus.Debugf("총 %d개의 binary log 파일을 찾았습니다.\n", len(binlogFiles))
	}

	// 시간대에 맞는 파일 찾기 (일관된 처리 방식)
	timeFinder := NewBinlogTimeFinder(ba.conn, ba.Config)

	if ba.Config.Verbose {
		logrus.Debugf("파일 검색 설정 - Workers: %d\n", ba.Config.Workers)
	}

	targetFiles, err := timeFinder.FindTargetFilesParallel(binlogFiles)
	if err != nil {
		return fmt.Errorf("대상 파일 찾기 실패: %v", err)
	}
	if len(targetFiles) == 0 {
		return fmt.Errorf("지정된 시간대(%s ~ %s)에 해당하는 binary log 파일을 찾을 수 없습니다",
			ba.Config.StartTime.Format("2006-01-02 15:04:05"),
			ba.Config.EndTime.Format("2006-01-02 15:04:05"))
	}

	if ba.Config.Verbose {
		logrus.Debugf("분석 대상 파일: %d개 (처리 순서)\n", len(targetFiles))
		for i, file := range targetFiles {
			logrus.Debugf("  %d. %s (크기: %d bytes)\n", i+1, file.Name, file.Size)
		}
	}

	// SQL 이벤트 추출
	sqlExtractor := NewSQLExtractor(ba.Config)
	defer sqlExtractor.Close()

	events, err := sqlExtractor.ExtractSQLEvents(targetFiles)
	if err != nil {
		return fmt.Errorf("SQL 이벤트 추출 실패: %v", err)
	}

	// 중복 이벤트 제거
	uniqueEvents := ba.removeDuplicateEvents(events)

	if ba.Config.Verbose {
		logrus.Debugf("중복 제거 전: %d개 이벤트, 중복 제거 후: %d개 이벤트\n", len(events), len(uniqueEvents))
	}

	// 결과 출력
	return ba.outputResults(uniqueEvents)
}

// MySQL 서버에 연결
func (ba *BinlogAnalyzer) connect() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", ba.Config.User, ba.Config.Password, ba.Config.Host, ba.Config.Port)
	var err error
	ba.conn, err = sql.Open("mysql", dsn)
	if err != nil {
		return err
	}

	return ba.conn.Ping()
}

// Binary log 파일 목록 가져오기
func (ba *BinlogAnalyzer) getBinlogFiles() ([]config.BinlogFile, error) {
	rows, err := ba.conn.Query("SHOW BINARY LOGS")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 컬럼 정보 확인
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	/*
		> SHOW BINARY LOGS;
			+------------------+-----------+-----------+
			| Log_name         | File_size | Encrypted |
			+------------------+-----------+-----------+
			| mysql-bin.000001 |         0 | No        |
			| mysql-bin.000002 |         0 | No        |
			| mysql-bin.000003 |      6490 | No        |
			+------------------+-----------+-----------+
	*/
	var files []config.BinlogFile
	for rows.Next() {
		var logName string
		var fileSize int64
		var encrypted interface{}

		if len(columns) == 2 {
			// MySQL 5.7 이하: Log_name, File_size
			if err := rows.Scan(&logName, &fileSize); err != nil {
				return nil, err
			}
		} else if len(columns) >= 3 {
			// MySQL 8.0: Log_name, File_size, Encrypted
			if err := rows.Scan(&logName, &fileSize, &encrypted); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("예상치 못한 SHOW BINARY LOGS 결과 컬럼 수: %d", len(columns))
		}

		files = append(files, config.BinlogFile{
			Name: logName,
			Size: fileSize,
		})
	}

	return files, nil
}

// 결과 출력
func (ba *BinlogAnalyzer) outputResults(events []config.SQLEvent) error {
	var output *os.File
	var err error

	if ba.Config.OutputFile != "" {
		output, err = os.Create(ba.Config.OutputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file: %v", err)
		}
		defer output.Close()
	} else {
		output = os.Stdout
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	green := "\033[32m"
	reset := "\033[0m"

	fmt.Fprintf(output, "%s# Binary Log Analysis Results\n", green)
	fmt.Fprintf(output, "# Time Range: %s ~ %s\n",
		ba.Config.StartTime.Format("2006-01-02 15:04:05"),
		ba.Config.EndTime.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(output, "# Total Events: %d\n\n", len(events))

	for _, event := range events {
		fmt.Fprintf(output, "# at %d\n", event.Position)
		fmt.Fprintf(output, "#%s server id %d  end_log_pos %d\n",
			event.Timestamp.Format("060102 15:04:05"), event.ServerId, event.Position)

		if event.Database != "" {
			fmt.Fprintf(output, "use %s;\n", event.Database)
		}

		fmt.Fprintf(output, "%s;\n\n", event.SQL)
	}
	fmt.Fprintf(output, "%s", reset)

	logrus.Infof("Analysis complete: %d SQL events", len(events))
	if ba.Config.OutputFile != "" {
		logrus.Infof("Results saved to %s", ba.Config.OutputFile)
	}

	return nil
}

// 중복 이벤트 제거 (end_log_pos + timestamp 기준)
func (ba *BinlogAnalyzer) removeDuplicateEvents(events []config.SQLEvent) []config.SQLEvent {
	seen := make(map[string]bool)
	var uniqueEvents []config.SQLEvent

	for _, event := range events {
		// 고유키: end_log_pos + timestamp 조합
		key := fmt.Sprintf("%d_%s", event.Position, event.Timestamp)

		if !seen[key] {
			seen[key] = true
			uniqueEvents = append(uniqueEvents, event)
		} else if ba.Config.Verbose {
			logrus.Debugf("중복 이벤트 제거: pos=%d, time=%s\n", event.Position, event.Timestamp)
		}
	}

	return uniqueEvents
}
