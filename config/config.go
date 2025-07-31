package config

import (
	"time"
)

// MySQL 연결 및 분석 설정
type Config struct {
	Host       string
	Port       int
	User       string
	Password   string
	StartTime  time.Time
	EndTime    time.Time
	OutputFile string
	Verbose    bool
	Workers    int
}

// Binary log 파일 정보
type BinlogFile struct {
	Name      string
	Size      int64
	StartTime time.Time
	EndTime   time.Time
}

// SQL 이벤트 정보
type SQLEvent struct {
	Timestamp time.Time
	EventType string
	Database  string
	SQL       string
	ServerId  uint32
	Position  uint32
}
