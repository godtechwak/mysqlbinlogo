# mysqlbinlogo

A tool to analyze Aurora MySQL Binary Logs and identify SQL statements executed within a specific time frame.
<img width="1536" height="1024" alt="mysqlbinlogo" src="https://github.com/user-attachments/assets/ae15478a-ba9f-413c-b751-f30c12ebe34d" />



## 🎯 Key Features

* **Time Range Search**: Extract only SQL statements for the exact start and end times you specify
* **Parallel Processing**: Fast analysis of large binary logs using multiple workers
* **Efficient File Selection**: Time-based pre-filtering to skip unnecessary files
* **Flexible Output**: Save results to stdout or an output file

## Installation

```bash
cd mysqlbinlogo
go build -o mysqlbinlogo
```

## Usage

### Basic Usage

```bash
./mysqlbinlogo \
    --host "your-aurora-host.rds.amazonaws.com" \
    --user "your_username" \
    --password "your_password" \
    --start-time "2024-01-15 09:00:00" \
    --end-time "2024-01-15 10:00:00" \
    --verbose
```

### Specify Output File

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

### Detailed Output Mode

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

### High-Performance Parallel Processing

```bash
# Parallel processing with 5 workers (reduces 12 files from 20s → 4s)
./mysqlbinlogo \
    --host your-aurora-endpoint.amazonaws.com \
    --user admin \
    --password your-password \
    --start-time "2024-01-15 10:00:00" \
    --end-time "2024-01-15 11:00:00" \
    --workers 5 \
    --verbose
```

## Options

| Option         | Short | Description                             | Required |
| -------------- | ----- | --------------------------------------- | -------- |
| `--host`       | `-H`  | MySQL host address                      | ✅        |
| `--port`       | `-P`  | MySQL port (default: 3306)              | ❌        |
| `--user`       | `-u`  | MySQL username                          | ✅        |
| `--password`   | `-p`  | MySQL password                          | ✅        |
| `--start-time` | `-s`  | Start time (YYYY-MM-DD HH\:MM\:SS)      | ✅        |
| `--end-time`   | `-e`  | End time (YYYY-MM-DD HH\:MM\:SS)        | ✅        |
| `--output`     | `-o`  | Output file path                        | ❌        |
| `--verbose`    | `-v`  | Show detailed output                    | ❌        |
| `--workers`    | `-w`  | Number of parallel workers (default: 3) | ❌        |

## Output Format

The output is similar to `mysqlbinlog`:

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

## Use Cases

### 1. Point-in-Time Recovery

Check what changes occurred before restoring the database to a specific point in time:

```bash
./mysqlbinlogo \
    --host aurora-cluster.amazonaws.com \
    --user admin \
    --password mypassword \
    --start-time "2024-01-15 09:55:00" \
    --end-time "2024-01-15 10:05:00" \
    --output before-incident.sql
```

### 2. Incident Analysis

Review all SQL statements executed during a specific time frame of a system failure or data anomaly:

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

### 3. Auditing and Tracking

Track all data changes executed within a specific time frame:

```bash
./mysqlbinlogo \
    --host aurora-cluster.amazonaws.com \
    --user admin \
    --password mypassword \
    --start-time "2024-01-15 00:00:00" \
    --end-time "2024-01-15 23:59:59" \
    --output daily-changes.sql
```

## Performance Optimization

* **Minimize Time Range**: Specify the smallest time range necessary
* **Smart File Filtering**: Check binary log file time ranges first to skip unnecessary files
* **Parallel Processing**: Use `--workers` to analyze multiple files concurrently (up to 5x speed)
* **Early Stop**: Automatically stop processing when files exceed the time range

### Worker Count Guide

* **2–5 files**: `--workers 1` (sequential)
* **6–10 files**: `--workers 3` (default)
* **11–20 files**: `--workers 5` (recommended)
* **20+ files**: `--workers 8` (max)

## Limitations

* Aurora MySQL Binary Logs must be enabled
* Adequate binary log retention period required
* Stable network connection required for large binary log files

## Troubleshooting

### Connection Error

```
MySQL connection failed: dial tcp: connection refused
```

* Verify host address and port
* Check MySQL port in the security group

### Permission Error

```
Failed to retrieve binary log files: access denied
```

* Ensure user has REPLICATION SLAVE privilege
* Example: `GRANT REPLICATION SLAVE ON *.* TO 'user'@'%';`

### Time Range Error

```
No binary log files found for the specified time range
```

* Check binary log retention period
* Confirm the specified time range is within the available binary logs

## Contributing

Feel free to suggest issues or improvements at any time.

## License

MIT License
