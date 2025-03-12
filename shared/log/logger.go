package log   //TODO: To be removed once we have logger plugin

import (
	"log"
	"os"
)

type Logger struct {
	logger *log.Logger
}

func NewLogger() *Logger {
	return &Logger{
		logger: log.New(os.Stdout, "LOG: ", log.Ldate|log.Ltime|log.Lshortfile),
	}
}

// Info logs
func (l *Logger) Info(v ...interface{}) {
	l.logger.SetPrefix("INFO: ")
	l.logger.Println(v...)
}

// Error logs
func (l *Logger) Error(v ...interface{}) {
	l.logger.SetPrefix("ERROR: ")
	l.logger.Println(v...)
}

func (l *Logger) Println(v ...interface{}) {
	l.logger.SetPrefix("ERROR: ")
	l.logger.Println(v...)
}

func (l *Logger) Fatalln(v ...interface{}) {
	l.logger.SetPrefix("FATAL: ")
	l.logger.Println(v...)
}

// Debug logs
func (l *Logger) Debug(v ...interface{}) {
	l.logger.SetPrefix("DEBUG: ")
	l.logger.Println(v...)
}

var Log = NewLogger()
