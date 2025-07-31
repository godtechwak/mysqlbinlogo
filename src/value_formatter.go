package src

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// 값을 SQL 문자열로 포맷
func (se *SQLExtractor) formatValue(val interface{}) string {
	if val == nil {
		return "NULL"
	}

	switch v := val.(type) {
	case string:
		// 문자열은 따옴표로 감싸고 특수문자 이스케이프
		escaped := strings.ReplaceAll(v, "'", "''")
		if len(escaped) > 50 {
			escaped = escaped[:47] + "..."
		}
		return fmt.Sprintf("'%s'", escaped)

	case []byte:
		// 바이트 배열을 문자열로 변환
		str := string(v)
		escaped := strings.ReplaceAll(str, "'", "''")
		if len(escaped) > 50 {
			escaped = escaped[:47] + "..."
		}
		return fmt.Sprintf("'%s'", escaped)

	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", v)

	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)

	case float32, float64:
		return fmt.Sprintf("%.6g", v)

	case bool:
		if v {
			return "1"
		}
		return "0"

	case time.Time:
		return fmt.Sprintf("'%s'", v.Format("2006-01-02 15:04:05"))

	default:
		// 기타 타입은 문자열로 변환
		str := fmt.Sprintf("%v", v)
		if len(str) > 50 {
			str = str[:47] + "..."
		}
		return fmt.Sprintf("'%s'", str)
	}
}

// 두 값이 같은지 비교
func (se *SQLExtractor) valuesEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// reflect.DeepEqual을 사용하되, 바이트 배열은 특별히 처리
	switch va := a.(type) {
	case []byte:
		if vb, ok := b.([]byte); ok {
			return string(va) == string(vb)
		}
	}

	return reflect.DeepEqual(a, b)
}
