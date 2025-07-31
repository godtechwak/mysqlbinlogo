package main

import (
	"io"
	"os"
	"time"

	"mysqlbinlogo/config"
	"mysqlbinlogo/src"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	host       string
	port       int
	user       string
	password   string
	startTime  string
	endTime    string
	outputFile string
	verbose    bool
	workers    int
)

func main() {
	// go-mysql 라이브러리의 로그를 완전히 숨김
	logrus.SetOutput(io.Discard)

	var rootCmd = &cobra.Command{
		Use:   "mysqlbinlogo",
		Short: "Aurora MySQL Binary Log Analyzer",
		Long:  `mysqlbinlogo is a tool that analyzes Aurora MySQL binary logs to identify SQL statements executed within a specific time frame.`,
		Run:   runBinlogAnalysis,
	}

	// CLI 플래그 정의
	rootCmd.Flags().StringVarP(&host, "host", "H", "", "MySQL host address (required)")
	rootCmd.Flags().IntVarP(&port, "port", "P", 3306, "MySQL port")
	rootCmd.Flags().StringVarP(&user, "user", "u", "", "MySQL user (required)")
	rootCmd.Flags().StringVarP(&password, "password", "p", "", "MySQL password (required)")
	rootCmd.Flags().StringVarP(&startTime, "start-time", "s", "", "Binary log start time (YYYY-MM-DD HH:MM:SS, required)")
	rootCmd.Flags().StringVarP(&endTime, "end-time", "e", "", "Binary log end time (YYYY-MM-DD HH:MM:SS, required)")
	rootCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Result file path (optional)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Detailed print")
	rootCmd.Flags().IntVarP(&workers, "workers", "w", 3, "Parallel workers")

	// 필수 플래그 설정
	rootCmd.MarkFlagRequired("host")
	rootCmd.MarkFlagRequired("user")
	rootCmd.MarkFlagRequired("password")
	rootCmd.MarkFlagRequired("start-time")
	rootCmd.MarkFlagRequired("end-time")

	if err := rootCmd.Execute(); err != nil {
		logrus.Fatalf("Command execution failed: %v", err)
	}
}

func runBinlogAnalysis(cmd *cobra.Command, args []string) {
	// startTime 형식 검증 (UTC 기준으로 파싱)
	startTimeObj, err := time.Parse("2006-01-02 15:04:05", startTime)
	if err != nil {
		logrus.Infof("시작 시간 형식이 올바르지 않습니다: %v\n", err)
		os.Exit(1)
	}
	// UTC로 명시적 설정
	startTimeUTC := startTimeObj.UTC()

	// endTime 형식 검증 (UTC 기준으로 파싱)
	endTimeObj, err := time.Parse("2006-01-02 15:04:05", endTime)
	if err != nil {
		logrus.Infof("종료 시간 형식이 올바르지 않습니다: %v\n", err)
		os.Exit(1)
	}
	// UTC로 명시적 설정
	endTimeUTC := endTimeObj.UTC()

	// endTime > startTime 체크
	if startTimeUTC.After(endTimeUTC) {
		logrus.Infof("시작 시간이 종료 시간보다 늦을 수 없습니다.")
		os.Exit(1)
	}

	if verbose {
		logrus.Infof("검색 시간 범위 (UTC): %s ~ %s\n",
			startTimeUTC.Format("2006-01-02 15:04:05"),
			endTimeUTC.Format("2006-01-02 15:04:05"))
	}

	// Binary log 분석
	analyzer := &src.BinlogAnalyzer{
		Config: config.Config{
			Host:       host,
			Port:       port,
			User:       user,
			Password:   password,
			StartTime:  startTimeUTC,
			EndTime:    endTimeUTC,
			OutputFile: outputFile,
			Verbose:    verbose,
			Workers:    workers,
		},
	}

	if err := analyzer.Analyze(); err != nil {
		logrus.Infof("Binary log 분석 중 오류 발생: %v\n", err)
		os.Exit(1)
	}
}
