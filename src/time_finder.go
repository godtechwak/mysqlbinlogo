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

// 시간 범위에 해당하는 binary log 파일들을 효율적으로 찾기
func (btf *BinlogTimeFinder) FindTargetFiles(files []config.BinlogFile) ([]config.BinlogFile, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("binary log 파일이 없습니다")
	}

	// 파일명 기준으로 정렬 (시간순)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	// 각 파일의 시간 범위 확인
	fileTimeRanges, err := btf.getFileTimeRanges(files)
	if err != nil {
		return nil, fmt.Errorf("파일 시간 범위 확인 실패: %v", err)
	}

	// 이진 탐색으로 시작 파일과 끝 파일 찾기
	startIdx := btf.findStartFile(fileTimeRanges)
	endIdx := btf.findEndFile(fileTimeRanges)

	if startIdx == -1 || endIdx == -1 || startIdx > endIdx {
		return nil, fmt.Errorf("지정된 시간 범위에 해당하는 파일을 찾을 수 없습니다")
	}

	// 대상 파일들 반환
	var targetFiles []config.BinlogFile
	for i := startIdx; i <= endIdx; i++ {
		targetFiles = append(targetFiles, files[i])
	}

	return targetFiles, nil
}

// 각 파일의 시간 범위를 확인
func (btf *BinlogTimeFinder) getFileTimeRanges(files []config.BinlogFile) ([]FileTimeRange, error) {
	var ranges []FileTimeRange

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

	syncer := replication.NewBinlogSyncer(cfg)
	defer syncer.Close()

	for _, file := range files {
		timeRange, err := btf.getFileTimeRange(syncer, file)
		if err != nil {
			if btf.config.Verbose {
				logrus.Debugf("파일 %s 시간 범위 확인 실패: %v\n", file.Name, err)
			}
			// 실패한 파일은 건너뛰고 계속 진행
			continue
		}
		ranges = append(ranges, timeRange)
	}

	return ranges, nil
}

// 단일 파일의 시간 범위 확인
func (btf *BinlogTimeFinder) getFileTimeRange(syncer *replication.BinlogSyncer, file config.BinlogFile) (FileTimeRange, error) {
	timeRange := FileTimeRange{
		FileName: file.Name,
		Size:     file.Size,
	}

	// Binary log 스트리밍 시작
	streamer, err := syncer.StartSync(mysql.Position{Name: file.Name, Pos: 4})
	if err != nil {
		return timeRange, fmt.Errorf("스트리밍 시작 실패: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 첫 번째 이벤트에서 시작 시간 추출
	for {
		select {
		case <-ctx.Done():
			return timeRange, fmt.Errorf("시간 초과")
		default:
			ev, err := streamer.GetEvent(ctx)
			if err != nil {
				return timeRange, fmt.Errorf("이벤트 읽기 실패: %v", err)
			}

			if ev.Header.Timestamp > 0 {
				timeRange.StartTime = time.Unix(int64(ev.Header.Timestamp), 0).UTC()
			}
		}
	}
}

// 시작 시간에 해당하는 첫 번째 파일 인덱스 찾기
func (btf *BinlogTimeFinder) findStartFile(ranges []FileTimeRange) int {
	for i, r := range ranges {
		// 파일의 끝 시간이 시작 시간보다 크거나 같으면 해당 파일부터 시작
		if r.EndTime.IsZero() || r.EndTime.After(btf.config.StartTime) || r.EndTime.Equal(btf.config.StartTime) {
			return i
		}
	}
	return -1
}

// 종료 시간에 해당하는 마지막 파일 인덱스 찾기
func (btf *BinlogTimeFinder) findEndFile(ranges []FileTimeRange) int {
	for i := len(ranges) - 1; i >= 0; i-- {
		r := ranges[i]
		// 파일의 시작 시간이 종료 시간보다 작거나 같으면 해당 파일까지 포함
		if r.StartTime.IsZero() || r.StartTime.Before(btf.config.EndTime) || r.StartTime.Equal(btf.config.EndTime) {
			return i
		}
	}
	return -1
}

// 파일의 시간 범위 정보
type FileTimeRange struct {
	FileName  string
	Size      int64
	StartTime time.Time
	EndTime   time.Time
}
