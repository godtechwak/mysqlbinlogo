package src

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"mysqlbinlogo/config"

	_ "github.com/go-sql-driver/mysql"
	"github.com/schollz/progressbar/v3"
)

// BinlogAnalyzer Binary log 분석기
type BinlogAnalyzer struct {
	Config config.Config
	conn   *sql.DB
}

// Analyze Binary log 분석 실행
func (ba *BinlogAnalyzer) Analyze() error {
	if ba.Config.Verbose {
		// verbose 모드에서는 로딩바 대신 상세 로그 출력
		fmt.Printf("분석 시작: %s ~ %s\n",
			ba.Config.StartTime.Format("2006-01-02 15:04:05"),
			ba.Config.EndTime.Format("2006-01-02 15:04:05"))
		fmt.Printf("MySQL 서버에 연결 중... %s:%d\n", ba.Config.Host, ba.Config.Port)
	}

	// verbose 모드가 아닐 때만 로딩바 사용
	var bar *progressbar.ProgressBar
	if !ba.Config.Verbose {
		// 단일 진행률바 (100단계 = 1%씩)
		bar = progressbar.NewOptions(100,
			progressbar.OptionSetDescription("분석 진행률"),
			progressbar.OptionSetWidth(50),
			progressbar.OptionShowCount(),
			progressbar.OptionEnableColorCodes(false),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "█",
				SaucerHead:    "█",
				SaucerPadding: "░",
				BarStart:      "[",
				BarEnd:        "]",
			}),
		)
	}

	// 1. MySQL 연결 (5%)
	if !ba.Config.Verbose {
		for i := 0; i < 3; i++ {
			bar.Add(1)
			bar.Describe("MySQL 연결 중...")
		}
	}

	if err := ba.connect(); err != nil {
		return fmt.Errorf("MySQL 연결 실패: %v", err)
	}
	defer ba.conn.Close()

	if !ba.Config.Verbose {
		for i := 0; i < 2; i++ {
			bar.Add(1)
			bar.Describe("MySQL 연결 완료")
		}
	} else {
		fmt.Println("MySQL 연결 완료")
	}

	// 2. Binary log 파일 목록 가져오기 및 대상 파일 검색 (10%)
	if !ba.Config.Verbose {
		for i := 0; i < 5; i++ {
			bar.Add(1)
			bar.Describe("바이너리 로그 파일 검색 중...")
		}
	} else {
		fmt.Println("바이너리 로그 파일 검색 중...")
	}

	binlogFiles, err := ba.getBinlogFiles()
	if err != nil {
		return fmt.Errorf("binary log 파일 목록 가져오기 실패: %v", err)
	}

	if ba.Config.Verbose {
		fmt.Printf("총 %d개의 binary log 파일을 찾았습니다.\n", len(binlogFiles))
	}

	// 시간대에 맞는 파일 찾기
	timeFinder := NewBinlogTimeFinder(ba.conn, ba.Config)

	if ba.Config.Verbose {
		fmt.Printf("파일 검색 설정 - Workers: %d\n", ba.Config.Workers)
	}

	targetFiles, err := timeFinder.FindTargetFilesParallel(binlogFiles)
	if err != nil {
		return fmt.Errorf("대상 파일 찾기 실패: %v", err)
	}

	if !ba.Config.Verbose {
		for i := 0; i < 5; i++ {
			bar.Add(1)
			bar.Describe("파일 검색 완료")
		}
	} else {
		fmt.Println("파일 검색 완료")
	}

	if len(targetFiles) == 0 {
		if !ba.Config.Verbose {
			bar.Finish()
		}
		fmt.Printf("\n\n지정된 시간대(%s ~ %s)에 해당하는 binary log 파일을 찾을 수 없습니다\n",
			ba.Config.StartTime.Format("2006-01-02 15:04:05"),
			ba.Config.EndTime.Format("2006-01-02 15:04:05"))
		return nil
	}

	if ba.Config.Verbose {
		fmt.Printf("분석 대상 파일: %d개 (처리 순서)\n", len(targetFiles))
		for i, file := range targetFiles {
			fmt.Printf("  %d. %s (크기: %d bytes)\n", i+1, file.Name, file.Size)
		}
	}

	// 3. SQL 이벤트 추출 (80%)
	sqlExtractor := NewSQLExtractor(ba.Config)
	defer sqlExtractor.Close()

	var allEvents []config.SQLEvent

	if !ba.Config.Verbose {
		progressPerFile := 80 / len(targetFiles) // 80%를 파일 개수로 나눔
		if progressPerFile < 5 {
			progressPerFile = 5
		}

		// 병렬 처리를 위한 채널과 고루틴 사용
		eventChan := make(chan []config.SQLEvent, len(targetFiles))
		errorChan := make(chan error, len(targetFiles))

		// 워커 수 결정 (파일 수와 설정된 워커 수 중 작은 값)
		workerCount := ba.Config.Workers
		if workerCount > len(targetFiles) {
			workerCount = len(targetFiles)
		}
		if workerCount < 1 {
			workerCount = 1
		}

		// 작업 채널 생성
		fileChan := make(chan config.BinlogFile, len(targetFiles))

		// 워커 고루틴들 시작
		var wg sync.WaitGroup
		for i := 0; i < workerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for file := range fileChan {
					events, err := sqlExtractor.ExtractFromSingleFile(file)
					if err != nil {
						errorChan <- err
					} else {
						eventChan <- events
					}
				}
			}()
		}

		// 파일들을 작업 채널에 전송
		go func() {
			defer close(fileChan)
			for _, file := range targetFiles {
				fileChan <- file
			}
		}()

		// 결과 수집 고루틴
		go func() {
			wg.Wait()
			close(eventChan)
			close(errorChan)
		}()

		// 진행률 업데이트와 결과 수집
		processedFiles := 0
		for processedFiles < len(targetFiles) {
			select {
			case events := <-eventChan:
				allEvents = append(allEvents, events...)
				processedFiles++

				// 진행률 업데이트
				for j := 0; j < progressPerFile; j++ {
					bar.Add(1)
					bar.Describe(fmt.Sprintf("파일 완료: %d/%d (%d개 이벤트)", processedFiles, len(targetFiles), len(events)))
				}
			case <-errorChan:
				processedFiles++
				// 에러는 조용히 무시하고 진행률만 업데이트
				for j := 0; j < progressPerFile; j++ {
					bar.Add(1)
					bar.Describe(fmt.Sprintf("파일 실패: %d/%d", processedFiles, len(targetFiles)))
				}
			}
		}
	} else {
		// verbose 모드에서는 로딩바 없이 직접 처리
		for i, file := range targetFiles {
			fmt.Printf("파일 처리 중: %s (%d/%d)\n", file.Name, i+1, len(targetFiles))

			events, err := sqlExtractor.ExtractFromSingleFile(file)

			if err != nil {
				fmt.Printf("파일 %s 처리 실패: %v (계속 진행)\n", file.Name, err)
			} else {
				allEvents = append(allEvents, events...)
				eventCount := 0
				if events != nil {
					eventCount = len(events)
				}
				fmt.Printf("파일 완료: %s (%d개 이벤트)\n", file.Name, eventCount)
			}
		}
	}

	if len(allEvents) == 0 {
		if !ba.Config.Verbose {
			bar.Finish()
		}
		fmt.Println("\n\n지정된 조건에 맞는 SQL 이벤트를 찾을 수 없습니다.")
		return nil
	}

	if !ba.Config.Verbose {
		// 남은 진행률 채우기 (100%까지) - 지연 없음
		currentProgress := 15 + (80 / len(targetFiles) * len(targetFiles)) // MySQL 연결(5) + 파일 검색(10) + 파일 처리(80)
		remaining := 100 - currentProgress
		if remaining > 0 {
			for i := 0; i < remaining; i++ {
				bar.Add(1)
				if i < remaining/2 {
					bar.Describe(fmt.Sprintf("결과 정리 중... (총 %d개 이벤트)", len(allEvents)))
				} else {
					bar.Describe("분석 완료")
				}
			}
			fmt.Printf("\n")
		}
	} else {
		fmt.Printf("결과 정리 중... (총 %d개 이벤트)\n", len(allEvents))
	}

	uniqueEvents, duplicateCount := ba.removeDuplicateEvents(allEvents)

	if ba.Config.Verbose {
		fmt.Printf("중복 제거 전: %d개 이벤트, 중복 제거 후: %d개 이벤트\n", len(allEvents), len(uniqueEvents))
	}

	// 진행률바 완료
	if !ba.Config.Verbose {
		bar.Finish()
	} else {
		fmt.Println("분석 완료")
	}

	// 결과 출력 (진행률바 완료 후, 개행 추가)
	fmt.Println() // 개행 추가
	err = ba.outputResults(uniqueEvents)
	if err != nil {
		return fmt.Errorf("결과 출력 실패: %v", err)
	}

	fmt.Printf("\n>> 총 %d개의 고유한 SQL 이벤트를 발견했습니다.\n", len(uniqueEvents))
	if duplicateCount > 0 {
		fmt.Printf(">> 중복 제거: %d개 → %d개 (총 %d개 중복 이벤트 제거)\n", len(allEvents), len(uniqueEvents), duplicateCount)
	} else {
		fmt.Printf(">> 중복 제거: %d개 → %d개 (중복 없음)\n", len(allEvents), len(uniqueEvents))
	}

	return nil
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

	fmt.Printf("%s", green)
	fmt.Fprintf(output, "# Binary Log Analysis Results\n")
	fmt.Fprintf(output, "# Time Range: %s ~ %s\n",
		ba.Config.StartTime.Format("2006-01-02 15:04:05"),
		ba.Config.EndTime.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(output, "# Total Events: %d\n\n", len(events))

	for _, event := range events {
		fmt.Fprintf(output, "# at %d\n", event.Position)
		fmt.Fprintf(output, "#%s server id %d  end_log_pos %d\n",
			event.Timestamp.Format("060102 15:04:05"), event.ServerId, event.Position)
		fmt.Fprintf(output, "# Binary Log File: %s\n", event.Filename)

		if event.Database != "" {
			fmt.Fprintf(output, "use %s;\n", event.Database)
		}

		fmt.Fprintf(output, "%s;\n\n", event.SQL)
	}
	fmt.Printf("%s", reset)

	logrus.Infof("Analysis complete: %d SQL events", len(events))
	if ba.Config.OutputFile != "" {
		logrus.Infof("Results saved to %s", ba.Config.OutputFile)
	}

	return nil
}

// 중복 이벤트 제거 (end_log_pos + timestamp 기준, 원본 파일 우선)
func (ba *BinlogAnalyzer) removeDuplicateEvents(events []config.SQLEvent) ([]config.SQLEvent, int) {
	// 이벤트를 파일명별로 그룹화하여 원본 파일 우선순위 결정
	eventGroups := make(map[string][]config.SQLEvent) // key: position_timestamp

	for _, event := range events {
		key := fmt.Sprintf("%d_%s", event.Position, event.Timestamp)
		eventGroups[key] = append(eventGroups[key], event)
	}

	var uniqueEvents []config.SQLEvent
	duplicateCount := 0

	for _, group := range eventGroups {
		if len(group) == 1 {
			// 중복 없는 이벤트
			uniqueEvents = append(uniqueEvents, group[0])
		} else {
			// 중복 이벤트들 - 원본 파일 우선 선택
			originalEvent := ba.selectOriginalEvent(group)
			uniqueEvents = append(uniqueEvents, originalEvent)
			duplicateCount += len(group) - 1

			if ba.Config.Verbose {
				logrus.Debugf("중복 이벤트 제거: pos=%d, time=%s, 원본=%s, 제거=%d개\n",
					originalEvent.Position, originalEvent.Timestamp, originalEvent.Filename, len(group)-1)
			}
		}
	}

	return uniqueEvents, duplicateCount
}

// 중복 이벤트들 중에서 원본 이벤트 선택
func (ba *BinlogAnalyzer) selectOriginalEvent(events []config.SQLEvent) config.SQLEvent {
	if len(events) == 0 {
		return config.SQLEvent{}
	}

	// 1. 먼저 Position이 가장 작은 이벤트를 찾음 (일반적으로 원본 이벤트는 Position이 작음)
	var originalEvent config.SQLEvent
	var minPosition uint32 = ^uint32(0) // 최대값으로 초기화

	for _, event := range events {
		if event.Position < minPosition {
			minPosition = event.Position
			originalEvent = event
		}
	}

	// 2. Position이 동일한 경우, 파일 번호가 가장 큰 것을 선택 (가장 최신 파일)
	var candidates []config.SQLEvent
	for _, event := range events {
		if event.Position == minPosition {
			candidates = append(candidates, event)
		}
	}

	if len(candidates) > 1 {
		// Position이 동일한 여러 파일이 있는 경우, 파일 번호가 가장 큰 것 선택
		var maxFileNum int = 0
		for _, event := range candidates {
			fileNum := ba.extractFileNumber(event.Filename)
			if fileNum > maxFileNum {
				maxFileNum = fileNum
				originalEvent = event
			}
		}
	}

	return originalEvent
}

// 파일명에서 번호 추출 (예: mysql-bin-changelog.000012 -> 12)
func (ba *BinlogAnalyzer) extractFileNumber(filename string) int {
	// mysql-bin-changelog.000012 형태에서 000012 부분 추출
	parts := strings.Split(filename, ".")
	if len(parts) >= 2 {
		lastPart := parts[len(parts)-1]
		// 앞의 0 제거
		lastPart = strings.TrimLeft(lastPart, "0")
		if lastPart == "" {
			lastPart = "0" // 모든 숫자가 0인 경우
		}
		if num, err := strconv.Atoi(lastPart); err == nil {
			return num
		}
	}
	return 999999 // 파싱 실패 시 큰 값 반환
}
