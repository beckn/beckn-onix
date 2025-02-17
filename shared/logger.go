package utils

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


var Log = NewLogger()

