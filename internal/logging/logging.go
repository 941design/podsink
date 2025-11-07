package logging

import (
	"log"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Configure sets up rotating file logging at the given path.
func Configure(path string) {
	log.SetOutput(&lumberjack.Logger{
		Filename:   path,
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   false,
	})
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
}
