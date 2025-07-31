# mysqlbinlogo

Aurora MySQL의 Binary Log를 분석하여 특정 시간대에 실행된 SQL 명령을 찾아내는 도구입니다.

## 🎯 주요 특징

- **시간 범위 검색**: 정확한 시작/종료 시간 지정으로 원하는 구간의 SQL만 추출
- **병렬 처리**: 다중 워커를 통한 빠른 대용량 binary log 분석
- **효율적 파일 선별**: 시간 기반 pre-filtering으로 불필요한 파일 스킵
- **다양한 출력 형식**: 표준 출력 또는 파일로 결과 저장

## 설치

```bash
cd mysqlbinlogo
go build -o mysqlbinlogo
```

## 사용법

### 기본 사용법

```bash
./mysqlbinlogo \
    --host "your-aurora-host.rds.amazonaws.com" \
    --user "your_username" \
    --password "your_password" \
    --start-time "2024-01-15 09:00:00" \
    --end-time "2024-01-15 10:00:00" \
    --verbose
```

### 출력 파일 지정

```bash
./mysqlbinlogo \
    --host your-aurora-endpoint.amazonaws.com \
    --port 3306 \
    --user admin \
    --password your-password \
    --start-time "2024-01-15 10:00:00" \
    --end-time "2024-01-15 11:00:00" \
    --output /tmp/binlog-analysis.sql
```

### 상세 출력 모드

```bash
./mysqlbinlogo \
    --host your-aurora-endpoint.amazonaws.com \
    --port 3306 \
    --user admin \
    --password your-password \
    --start-time "2024-01-15 10:00:00" \
    --end-time "2024-01-15 11:00:00" \
    --verbose
```

### 고성능 병렬 처리

```bash
# 5개 워커로 병렬 처리 (12개 파일을 20초 → 4초로 단축)
./mysqlbinlogo \
    --host your-aurora-endpoint.amazonaws.com \
    --user admin \
    --password your-password \
    --start-time "2024-01-15 10:00:00" \
    --end-time "2024-01-15 11:00:00" \
    --workers 5 \
    --verbose
```

## 옵션

| 옵션          | 축약 | 설명                           | 필수 |
|---------------|------|--------------------------------|------|
| `--host`      | `-H` | MySQL 호스트 주소              | ✅   |
| `--port`      | `-P` | MySQL 포트 (기본값: 3306)      | ❌   |
| `--user`      | `-u` | MySQL 사용자명                 | ✅   |
| `--password`  | `-p` | MySQL 비밀번호                 | ✅   |
| `--start-time`| `-s` | 시작 시간 (YYYY-MM-DD HH:MM:SS)| ✅   |
| `--end-time`  | `-e` | 종료 시간 (YYYY-MM-DD HH:MM:SS)| ✅   |
| `--output`    | `-o` | 결과 파일 경로                 | ❌   |
| `--verbose`   | `-v` | 상세 출력                      | ❌   |
| `--workers`   | `-w` | 병렬 처리 워커 수 (기본값: 3)  | ❌   |

## 출력 형식

mysqlbinlog와 동일한 형식으로 출력됩니다:

```sql
# Binary Log Analysis Results
# Time Range: 2024-01-15 10:00:00 ~ 2024-01-15 11:00:00
# Total Events: 150

# at 4567
#240115 10:15:30 server id 1  end_log_pos 4623
use mydb;
INSERT INTO users (name, email) VALUES ('John', 'john@example.com');

# at 4623
#240115 10:16:45 server id 1  end_log_pos 4789
use mydb;
UPDATE users SET status = 'active' WHERE id = 123 -- 1 row(s) affected;
```

## 사용 사례

### 1. 특정 시점 복원

데이터베이스를 특정 시점으로 복원하기 전에 해당 시점에 어떤 변경사항이 있었는지 확인:

```bash
./mysqlbinlogo \
    --host aurora-cluster.amazonaws.com \
    --user admin \
    --password mypassword \
    --start-time "2024-01-15 09:55:00" \
    --end-time "2024-01-15 10:05:00" \
    --output before-incident.sql
```

### 2. 문제 발생 시점 분석

시스템 장애나 데이터 이상이 발생했을 때 해당 시간대의 모든 SQL 명령 확인:

```bash
./mysqlbinlogo \
    --host "aurora-cluster.cluster-xxxxx.us-west-2.rds.amazonaws.com" \
    --user "admin" \
    --password "your_password" \
    --start-time "2024-01-15 14:30:00" \
    --end-time "2024-01-15 14:35:00" \
    --output "analysis_result.sql" \
    --verbose
```

### 3. 감사 및 추적

특정 시간대에 실행된 모든 데이터 변경 작업 추적:

```bash
./mysqlbinlogo \
    --host aurora-cluster.amazonaws.com \
    --user admin \
    --password mypassword \
    --start-time "2024-01-15 00:00:00" \
    --end-time "2024-01-15 23:59:59" \
    --output daily-changes.sql
```

## 성능 최적화

- **시간 범위 최소화**: 필요한 최소한의 시간 범위만 지정
- **스마트 파일 선별**: Binary Log 파일의 시간 범위를 먼저 확인하여 불필요한 파일 스킵
- **병렬 처리**: `--workers` 옵션으로 여러 파일을 동시에 검사 (최대 5배 성능 향상)
- **역방향 검색**: 최근 문제 조사 시 `--reverse` 옵션으로 최신 파일부터 효율적 검색
- **조기 종료**: 시간 범위를 벗어난 파일 발견 시 자동으로 검색 중단

### 워커 수 가이드
- **2-5개 파일**: `--workers 1` (순차 처리)
- **6-10개 파일**: `--workers 3` (기본값)  
- **11-20개 파일**: `--workers 5` (권장)
- **20개 이상**: `--workers 8` (최대)

## 제한사항

- Aurora MySQL의 Binary Log가 활성화되어 있어야 함
- 충분한 Binary Log retention 시간이 설정되어 있어야 함
- 네트워크 연결이 안정적이어야 함 (큰 Binary Log 파일 처리 시)

## 트러블슈팅

### 연결 오류
```
MySQL 연결 실패: dial tcp: connection refused
```
- 호스트 주소와 포트가 올바른지 확인
- 보안 그룹에서 MySQL 포트가 열려있는지 확인

### 권한 오류
```
Binary log 파일 목록 가져오기 실패: access denied
```
- 사용자에게 REPLICATION SLAVE 권한이 있는지 확인
- `GRANT REPLICATION SLAVE ON *.* TO 'user'@'%';`

### 시간 범위 오류
```
지정된 시간대에 해당하는 binary log 파일을 찾을 수 없습니다
```
- Binary Log retention 기간 확인
- 지정한 시간이 현재 Binary Log 범위 내에 있는지 확인

## 기여

이슈나 개선사항이 있으시면 언제든지 제안해 주세요.

## 라이선스

MIT License 