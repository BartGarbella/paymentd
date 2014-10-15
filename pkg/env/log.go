package env

import (
	"bytes"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"gopkg.in/inconshreveable/log15.v2"
	golog "log"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	logBufferSize  = 256
	timeFormat     = "2006-01-02T15:04:05-0700"
	floatFormat    = 'f'
	floatPrecision = 3
	errorKey       = "ERROR"
)

// logging prefixes for different log levels
// see <http://0pointer.de/public/systemd-man/sd-daemon.html>
const (
	sdCrit    = "<2>"
	sdErr     = "<3>"
	sdWarning = "<4>"
	sdInfo    = "<6>"
	sdDebug   = "<7>"
)

// Log is the default logger. It has to be initialized through
var Log log15.Logger

func init() {
	// adjust the logging environment and set the default log15.Logger for further
	// use.
	//
	// We follow the new-style daemons approach
	// see <http://0pointer.de/public/systemd-man/daemon.html#New-Style%20Daemons>
	Log = log15.New()
	Log.SetHandler(log15.StreamHandler(os.Stderr, DaemonFormat()))
	golog.SetOutput(logBridge{Log})
	err := mysql.SetLogger(mysqlLog{})
	if err != nil {
		Log.Crit("error setting up mysql log", log15.Ctx{"err": err})
	}
}

// logBridge acts as a Writer for the log pkg
// It will log to log15
type logBridge struct {
	log log15.Logger
}

// logBridge Writer implementation
// will log all log pkg messages as log15.Info messages
func (l logBridge) Write(msg []byte) (int, error) {
	l.log.Info("log pkg message", log15.Ctx{"message": string(msg)})
	return len(msg), nil
}

var bufferPool = &sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, logBufferSize))
	},
}

// DaemonFormat returns a log15.Format, which produces records which can be forwarded to
// syslog by the init system
func DaemonFormat() log15.Format {
	return log15.FormatFunc(func(r *log15.Record) (b []byte) {
		common := []interface{}{r.KeyNames.Time, r.Time, r.KeyNames.Lvl, r.Lvl, r.KeyNames.Msg, r.Msg}
		buf := bufferPool.Get().(*bytes.Buffer)
		buf.Reset()
		logLevel(buf, r.Lvl)
		logRecord(buf, append(common, r.Ctx...))
		b = buf.Bytes()
		bufferPool.Put(buf)
		return
	})
}

func logLevel(buf *bytes.Buffer, lvl log15.Lvl) {
	switch lvl {
	case log15.LvlCrit:
		buf.WriteString(sdCrit)
	case log15.LvlError:
		buf.WriteString(sdErr)
	case log15.LvlWarn:
		buf.WriteString(sdWarning)
	case log15.LvlInfo:
		buf.WriteString(sdInfo)
	case log15.LvlDebug:
		buf.WriteString(sdDebug)
	}
}

func logRecord(buf *bytes.Buffer, ctx []interface{}) {
	for i := 0; i < len(ctx); i += 2 {
		if i != 0 {
			buf.WriteByte(' ')
		}
		k, ok := ctx[i].(string)
		v := logValue(ctx[i+1])
		if !ok {
			k, v = errorKey, logValue(k)
		}

		fmt.Fprintf(buf, "%s=%s", k, v)
	}
	buf.WriteByte('\n')
}

func logValue(value interface{}) string {
	if value == nil {
		return "nil"
	}

	switch v := value.(type) {
	case time.Time:
		return v.Format(timeFormat)
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	case float32:
		return strconv.FormatFloat(float64(v), floatFormat, floatPrecision, 64)
	case float64:
		return strconv.FormatFloat(v, floatFormat, floatPrecision, 64)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v)
	case string:
		return escapeString(v)
	default:
		return escapeString(fmt.Sprintf("%+v", v))
	}
}

func escapeString(s string) (eStr string) {
	needQuotes := false
	e := bufferPool.Get().(*bytes.Buffer)
	e.Reset()
	e.WriteByte('"')
	for _, r := range s {
		if r <= ' ' || r == '=' || r == '"' {
			needQuotes = true
		}

		switch r {
		case '\\', '"':
			e.WriteByte('\\')
			e.WriteByte(byte(r))
		case '\n':
			e.WriteByte('\\')
			e.WriteByte('n')
		case '\r':
			e.WriteByte('\\')
			e.WriteByte('r')
		case '\t':
			e.WriteByte('\\')
			e.WriteByte('t')
		default:
			e.WriteRune(r)
		}
	}
	e.WriteByte('"')
	start, stop := 0, e.Len()
	if !needQuotes {
		start, stop = 1, stop-1
	}
	eStr = string(e.Bytes()[start:stop])
	bufferPool.Put(e)
	return
}

type mysqlLog struct{}

func (m mysqlLog) Print(v ...interface{}) {
	Log.Warn("mysql log", log15.Ctx{"mysqlLog": v})
}
