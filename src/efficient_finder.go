package src

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	"mysqlbinlogo/config"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
)

// 시간 기반 binary log 파일 찾기
type BinlogTimeFinder struct {
	conn   *sql.DB
	config config.Config
}

// 새 타임 파인더 생성
func NewBinlogTimeFinder(conn *sql.DB, cfg config.Config) *BinlogTimeFinder {
	return &BinlogTimeFinder{
		conn:   conn,
		config: cfg,
	}
}

// 파일의 시간 범위 정보
type FileTimeRange struct {
	FileName  string
	Size      int64
	StartTime time.Time
	EndTime   time.Time
}

// 효율적으로 시간 범위에 해당하는 파일들만 선별
func (btf *BinlogTimeFinder) FindTargetFilesEfficient(files []config.BinlogFile) ([]config.BinlogFile, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("binary log 파일이 없습니다")
	}

	if btf.config.Verbose {
		logrus.Debugf("총 %d개의 binary log 파일 중 시간 범위에 맞는 파일 검색 중...\n", len(files))
	}

	// 파일명 기준으로 순방향 정렬 (오래된 파일부터)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	var targetFiles []config.BinlogFile

	// MySQL 복제 설정
	cfg := replication.BinlogSyncerConfig{
		ServerID: 100,
		Flavor:   "mysql",
		Host:     btf.config.Host,
		Port:     uint16(btf.config.Port),
		User:     btf.config.User,
		Password: btf.config.Password,
		Logger:   &config.NullLogger{},
	}

	// 각 파일의 시간 범위를 빠르게 확인
	for i, file := range files {
		if btf.config.Verbose {
			logrus.Debugf("파일 %d/%d 검사 중: %s\n", i+1, len(files), file.Name)
		}

		// 새로운 syncer로 파일 시간 범위 확인
		syncer := replication.NewBinlogSyncer(cfg)

		timeRange, err := btf.getFileTimeRangeQuick(syncer, file)

		if err != nil {
			if btf.config.Verbose {
				logrus.Debugf("파일 %s 시간 범위 확인 실패: %v (스킵)\n", file.Name, err)
			}
			continue
		}

		if btf.config.Verbose {
			logrus.Debugf("파일 %s: %s ~ %s\n", file.Name,
				timeRange.StartTime.Format("2006-01-02 15:04:05"),
				timeRange.EndTime.Format("2006-01-02 15:04:05"))
		}

		// 시간 범위 확인
		if btf.isFileInTimeRange(timeRange) {
			targetFiles = append(targetFiles, file)
			if btf.config.Verbose {
				logrus.Debugf("파일 %s이 시간 범위에 포함됨\n", file.Name)
			}
		} else {
			if btf.config.Verbose {
				logrus.Debugf("파일 %s은 시간 범위 밖 (스킵)\n", file.Name)
			}
		}

		// 성능 최적화: 조기 종료 조건 (순방향)
		// 현재 파일의 시작 시간이 종료 시간보다 늦으면 종료
		if !timeRange.StartTime.IsZero() && timeRange.StartTime.After(btf.config.EndTime) {
			if btf.config.Verbose {
				logrus.Debugf("파일 %s의 시작 시간이 검색 종료 시간보다 늦으므로 더 이상 확인하지 않음\n", file.Name)
			}
			break
		}
	}

	if btf.config.Verbose {
		logrus.Debugf("최종 선별된 파일: %d개\n", len(targetFiles))
		for _, file := range targetFiles {
			logrus.Debugf("  - %s\n", file.Name)
		}
	}

	return targetFiles, nil
}

// 파일의 시간 범위를 빠르게 확인 (특정 파일만 처리, 다른 파일로 넘어가지 않음)
func (btf *BinlogTimeFinder) getFileTimeRangeQuick(syncer *replication.BinlogSyncer, file config.BinlogFile) (FileTimeRange, error) {
	timeRange := FileTimeRange{
		FileName: file.Name,
		Size:     file.Size,
	}

	// Binary log 스트리밍 시작 - 특정 파일의 시작 위치에서
	streamer, err := syncer.StartSync(mysql.Position{Name: file.Name, Pos: 4})
	if err != nil {
		return timeRange, fmt.Errorf("스트리밍 시작 실패: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var firstTimestamp, lastTimestamp uint32
	eventCount := 0
	maxEvents := 50 // 50개 이벤트로 제한

	// 시작 시간 찾기
	for eventCount < maxEvents {
		select {
		case <-ctx.Done():
			if firstTimestamp > 0 {
				timeRange.StartTime = time.Unix(int64(firstTimestamp), 0).UTC()
			}
			return timeRange, nil
		default:
			ev, err := streamer.GetEvent(ctx)
			if err != nil {
				// 파일 끝에 도달하거나 오류
				if firstTimestamp > 0 {
					timeRange.StartTime = time.Unix(int64(firstTimestamp), 0).UTC()
					timeRange.EndTime = time.Unix(int64(lastTimestamp), 0).UTC()
				}
				return timeRange, nil
			}

			// 현재 이벤트가 다른 파일로 넘어갔는지 확인
			if ev.Header.LogPos > 0 {
				// 파일 크기를 초과했거나 다른 파일로 넘어간 경우 종료
				// LogPos는 이벤트의 끝 위치이므로 파일 크기보다 클 수 있음
				// 대신 이벤트 크기를 고려하여 판단
				if ev.Header.LogPos > uint32(file.Size) && ev.Header.EventSize > 0 {
					// 이벤트 크기가 파일 크기를 초과하는 경우에만 종료
					if ev.Header.LogPos-ev.Header.EventSize > uint32(file.Size) {
						if btf.config.Verbose {
							logrus.Debugf("파일 %s 경계 도달, 처리 종료 (LogPos: %d, EventSize: %d, FileSize: %d)\n",
								file.Name, ev.Header.LogPos, ev.Header.EventSize, file.Size)
						}
						break
					}
				}
			}

			if ev.Header.Timestamp > 0 {
				if firstTimestamp == 0 {
					firstTimestamp = ev.Header.Timestamp
				}
				lastTimestamp = ev.Header.Timestamp
			}
			eventCount++
		}
	}

	// 시작 시간 설정
	if firstTimestamp > 0 {
		timeRange.StartTime = time.Unix(int64(firstTimestamp), 0).UTC()
	}

	// 마지막 이벤트 찾기 (샘플링 방식, 파일 경계 내에서만)
	sampleCount := 0
	maxSamples := 50

	for sampleCount < maxSamples {
		select {
		case <-ctx.Done():
			if lastTimestamp > 0 {
				timeRange.EndTime = time.Unix(int64(lastTimestamp), 0).UTC()
			}
			return timeRange, nil
		default:
			ev, err := streamer.GetEvent(ctx)
			if err != nil {
				// 파일 끝 도달
				if lastTimestamp > 0 {
					timeRange.EndTime = time.Unix(int64(lastTimestamp), 0).UTC()
				}
				return timeRange, nil
			}

			// 파일 경계 확인
			if ev.Header.LogPos > uint32(file.Size) && ev.Header.EventSize > 0 {
				// 이벤트 크기가 파일 크기를 초과하는 경우에만 종료
				if ev.Header.LogPos-ev.Header.EventSize > uint32(file.Size) {
					if btf.config.Verbose {
						logrus.Debugf("파일 %s 경계 도달, 샘플링 종료 (LogPos: %d, EventSize: %d, FileSize: %d)\n",
							file.Name, ev.Header.LogPos, ev.Header.EventSize, file.Size)
					}
					break
				}
			}

			if ev.Header.Timestamp > 0 {
				lastTimestamp = ev.Header.Timestamp
				sampleCount++
			}
		}
	}

	// 마지막 타임스탬프 설정
	if lastTimestamp > 0 {
		timeRange.EndTime = time.Unix(int64(lastTimestamp), 0).UTC()
	}

	return timeRange, nil
}

// 파일이 시간 범위에 포함되는지 확인
func (btf *BinlogTimeFinder) isFileInTimeRange(fileRange FileTimeRange) bool {
	// 파일 시간 정보가 없으면 일단 포함 (안전을 위해)
	if fileRange.StartTime.IsZero() && fileRange.EndTime.IsZero() {
		if btf.config.Verbose {
			logrus.Debugf("파일 %s: 시간 정보 없음, 포함으로 처리\n", fileRange.FileName)
		}
		return true
	}

	// 시간 범위가 매우 넓은 경우 (24시간 이상) 일단 포함
	timeDiff := fileRange.EndTime.Sub(fileRange.StartTime)
	if timeDiff > 24*time.Hour {
		if btf.config.Verbose {
			logrus.Debugf("파일 %s: 시간 범위가 넓음 (%.2f시간), 포함으로 처리\n",
				fileRange.FileName, timeDiff.Hours())
		}
		return true
	}

	// 버퍼 시간 추가 (6시간 전후로 확장)
	bufferTime := 6 * time.Hour
	searchStartTime := btf.config.StartTime.Add(-bufferTime)
	searchEndTime := btf.config.EndTime.Add(bufferTime)

	// 파일의 끝 시간이 검색 시작 시간보다 이르면 제외
	if !fileRange.EndTime.IsZero() && fileRange.EndTime.Before(searchStartTime) {
		if btf.config.Verbose {
			logrus.Debugf("파일 %s: 끝 시간(%s)이 검색 시작 시간(%s)보다 이름\n",
				fileRange.FileName,
				fileRange.EndTime.Format("2006-01-02 15:04:05"),
				searchStartTime.Format("2006-01-02 15:04:05"))
		}
		return false
	}

	// 파일의 시작 시간이 검색 끝 시간보다 늦으면 제외
	if !fileRange.StartTime.IsZero() && fileRange.StartTime.After(searchEndTime) {
		if btf.config.Verbose {
			logrus.Debugf("파일 %s: 시작 시간(%s)이 검색 끝 시간(%s)보다 늦음\n",
				fileRange.FileName,
				fileRange.StartTime.Format("2006-01-02 15:04:05"),
				searchEndTime.Format("2006-01-02 15:04:05"))
		}
		return false
	}

	// 그 외의 경우는 모두 포함 (겹치는 부분이 있음)
	if btf.config.Verbose {
		logrus.Debugf("파일 %s: 시간 범위에 포함됨 (겹치는 부분 존재)\n", fileRange.FileName)
	}
	return true
}
