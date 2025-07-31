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
	Filename  string // 이벤트가 발견된 바이너리 로그 파일명
}

// NullLogger implements loggers.Advanced interface to discard all logs
type NullLogger struct{}

// 기본 로그 메소드들
func (l *NullLogger) Debug(args ...interface{}) {}
func (l *NullLogger) Info(args ...interface{})  {}
func (l *NullLogger) Warn(args ...interface{})  {}
func (l *NullLogger) Error(args ...interface{}) {}
func (l *NullLogger) Fatal(args ...interface{}) {}

// 포맷 로그 메소드들
func (l *NullLogger) Debugf(format string, args ...interface{}) {}
func (l *NullLogger) Infof(format string, args ...interface{})  {}
func (l *NullLogger) Warnf(format string, args ...interface{})  {}
func (l *NullLogger) Errorf(format string, args ...interface{}) {}
func (l *NullLogger) Fatalf(format string, args ...interface{}) {}

// 줄바꿈 로그 메소드들
func (l *NullLogger) Debugln(args ...interface{}) {}
func (l *NullLogger) Infoln(args ...interface{})  {}
func (l *NullLogger) Warnln(args ...interface{})  {}
func (l *NullLogger) Errorln(args ...interface{}) {}
func (l *NullLogger) Fatalln(args ...interface{}) {}

// Panic 메소드들 (에러 발생 방지)
func (l *NullLogger) Panic(args ...interface{})                 {}
func (l *NullLogger) Panicf(format string, args ...interface{}) {}
func (l *NullLogger) Panicln(args ...interface{})               {}

// Print 메소드들
func (l *NullLogger) Print(args ...interface{})                 {}
func (l *NullLogger) Printf(format string, args ...interface{}) {}
func (l *NullLogger) Println(args ...interface{})               {}

// 추가 메소드들 (더 완전한 억제)
func (l *NullLogger) Log(args ...interface{})                 {}
func (l *NullLogger) Logf(format string, args ...interface{}) {}
func (l *NullLogger) Logln(args ...interface{})               {}
func (l *NullLogger) Write(p []byte) (n int, err error)       { return len(p), nil }
func (l *NullLogger) Close() error                            { return nil }

// go-mysql 라이브러리 특화 메소드들
func (l *NullLogger) SetLevel(level interface{})                           {}
func (l *NullLogger) GetLevel() interface{}                                { return "fatal" }
func (l *NullLogger) IsLevelEnabled(level interface{}) bool                { return false }
func (l *NullLogger) WithField(key string, value interface{}) interface{}  { return l }
func (l *NullLogger) WithFields(fields map[string]interface{}) interface{} { return l }
func (l *NullLogger) WithError(err error) interface{}                      { return l }
