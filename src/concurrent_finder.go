package src

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"mysqlbinlogo/config"

	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/sirupsen/logrus"
)

// 파일 검색 작업
type FileSearchJob struct {
	File  config.BinlogFile
	Index int // 원래 순서 유지를 위한 인덱스
}

// 파일 검색 결과
type FileSearchResult struct {
	File      config.BinlogFile
	TimeRange FileTimeRange
	Index     int
	Error     error
}

// 병렬 파일 검색
func (btf *BinlogTimeFinder) FindTargetFilesConcurrent(files []config.BinlogFile) ([]config.BinlogFile, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("binary log 파일이 없습니다")
	}

	if btf.config.Verbose {
		logrus.Debugf("총 %d개의 binary log 파일 중 시간 범위에 맞는 파일 검색 중... (워커: %d개)\n", len(files), btf.config.Workers)
	}

	// 파일명 기준으로 순방향 정렬 (오래된 파일부터)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	// 워커 수가 파일 수보다 많을 경우 파일 수와 동일하게 조정한다.
	workerCount := btf.config.Workers
	if workerCount > len(files) {
		workerCount = len(files)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	// 채널 생성
	jobs := make(chan FileSearchJob, len(files))
	results := make(chan FileSearchResult, len(files))

	// 워커 풀 시작 - 동적 작업 분배 방식
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go btf.searchWorkerDynamic(jobs, results, &wg, i+1)
	}

	// 작업 분배
	go func() {
		defer close(jobs)
		for i, file := range files {
			jobs <- FileSearchJob{File: file, Index: i}
		}
	}()

	// 결과 수집
	go func() {
		wg.Wait()
		close(results)
	}()

	// 결과 처리
	return btf.processSearchResults(results, len(files))
}

// 동적 파일 검색 워커 - 작업이 끝난 워커가 남은 파일들을 처리
func (btf *BinlogTimeFinder) searchWorkerDynamic(jobs <-chan FileSearchJob, results chan<- FileSearchResult, wg *sync.WaitGroup, workerId int) {
	defer wg.Done()

	for job := range jobs {
		if btf.config.Verbose {
			logrus.Debugf("워커 %d에서 파일 %d 검사 중: %s\n", workerId, job.Index+1, job.File.Name)
		}

		// 각 파일마다 새로운 syncer 생성 (독립적인 연결 보장)
		cfg := replication.BinlogSyncerConfig{
			ServerID: uint32(100 + workerId), // 워커별로 다른 ServerID 사용
			Flavor:   "mysql",
			Host:     btf.config.Host,
			Port:     uint16(btf.config.Port),
			User:     btf.config.User,
			Password: btf.config.Password,
			Logger:   &config.NullLogger{},
		}
		syncer := replication.NewBinlogSyncer(cfg)

		// 재시도 로직으로 안정성 향상
		var timeRange FileTimeRange
		var err error
		maxRetries := 10

		for retry := 0; retry < maxRetries; retry++ {
			timeRange, err = btf.getFileTimeRangeQuick(syncer, job.File)
			if err == nil {
				break
			}

			if retry < maxRetries-1 {
				if btf.config.Verbose {
					logrus.Debugf("워커 %d에서 파일 %s 재시도 중 (%d/%d): %v\n", workerId, job.File.Name, retry+1, maxRetries, err)
				}
				// 잠시 대기 후 재시도
				time.Sleep(100 * time.Millisecond)
			}
		}

		// syncer 즉시 닫기 (리소스 정리)
		//syncer.Close()

		result := FileSearchResult{
			File:      job.File,
			TimeRange: timeRange,
			Index:     job.Index,
			Error:     err,
		}

		results <- result
	}
}

// 검색 결과 처리 및 필터링
func (btf *BinlogTimeFinder) processSearchResults(results <-chan FileSearchResult, totalFiles int) ([]config.BinlogFile, error) {
	var allResults []FileSearchResult

	// 모든 결과 수집
	for result := range results {
		if result.Error != nil {
			if btf.config.Verbose {
				logrus.Debugf("파일 %s 시간 범위 확인 실패: %v (스킵)\n", result.File.Name, result.Error)
			}
			continue
		}
		allResults = append(allResults, result)
	}

	if btf.config.Verbose {
		logrus.Debugf("성공적으로 시간 범위를 확인한 파일: %d개\n", len(allResults))
	}

	// 파일명으로 순방향 정렬하여 일관된 순서 보장
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].File.Name < allResults[j].File.Name
	})

	// 시간 범위 확인 (모든 파일 처리)
	var targetFiles []config.BinlogFile

	if btf.config.Verbose {
		logrus.Debugf("검색 시간 범위: %s ~ %s\n",
			btf.config.StartTime.Format("2006-01-02 15:04:05"),
			btf.config.EndTime.Format("2006-01-02 15:04:05"))
	}

	for _, result := range allResults {
		timeRange := result.TimeRange

		if btf.config.Verbose {
			logrus.Debugf("파일 %s: %s ~ %s\n", result.File.Name,
				timeRange.StartTime.Format("2006-01-02 15:04:05"),
				timeRange.EndTime.Format("2006-01-02 15:04:05"))
		}

		// 시간 범위 확인
		if btf.isFileInTimeRange(timeRange) {
			targetFiles = append(targetFiles, result.File)
			if btf.config.Verbose {
				logrus.Debugf("파일 %s이 시간 범위에 포함됨\n", result.File.Name)
			}
		} else {
			if btf.config.Verbose {
				logrus.Debugf("파일 %s은 시간 범위 밖 (skip)\n", result.File.Name)
			}
		}
	}

	if btf.config.Verbose {
		logrus.Debugf("최종 선별된 파일: %d개\n", len(targetFiles))
		for i, file := range targetFiles {
			logrus.Debugf("  %d. %s\n", i+1, file.Name)
		}
	}

	return targetFiles, nil
}

// 병렬 처리 버전의 public 메서드
func (btf *BinlogTimeFinder) FindTargetFilesParallel(files []config.BinlogFile) ([]config.BinlogFile, error) {
	// 워커 수가 1이면 순차 처리
	if btf.config.Workers <= 1 {
		if btf.config.Verbose {
			logrus.Debugf("워커 수가 1이므로 순차 처리 모드로 실행합니다.\n")
		}
		return btf.FindTargetFilesEfficient(files)
	}

	// 병렬 처리
	if btf.config.Verbose {
		logrus.Debugf("병렬 처리 모드로 실행합니다. (워커: %d개)\n", btf.config.Workers)
	}
	return btf.FindTargetFilesConcurrent(files)
}
