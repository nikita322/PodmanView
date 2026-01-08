package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// LogLevel определяет уровень логирования
type LogLevel int

const (
	LevelInfo LogLevel = iota
	LevelError
	LevelFatal
)

// Logger - кастомный логгер с поддержкой записи в файлы
type Logger struct {
	infoLogger  *log.Logger
	errorLogger *log.Logger
	logDir      string
	appFile     *os.File
	errorFile   *os.File
	mu          sync.Mutex
}

// New создает новый логгер с указанной директорией для логов
func New(logDir string) (*Logger, error) {
	// Создаем директорию для логов если её нет
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Открываем файлы для логов
	appFile, err := os.OpenFile(
		filepath.Join(logDir, "app.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open app.log: %w", err)
	}

	errorFile, err := os.OpenFile(
		filepath.Join(logDir, "error.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		appFile.Close()
		return nil, fmt.Errorf("failed to open error.log: %w", err)
	}

	// Создаем MultiWriter для дублирования вывода в консоль и файл
	appWriter := io.MultiWriter(os.Stdout, appFile)
	errorWriter := io.MultiWriter(os.Stderr, errorFile)

	logger := &Logger{
		infoLogger:  log.New(appWriter, "", log.LstdFlags),
		errorLogger: log.New(errorWriter, "ERROR: ", log.LstdFlags|log.Lshortfile),
		logDir:      logDir,
		appFile:     appFile,
		errorFile:   errorFile,
	}

	return logger, nil
}

// Close закрывает файлы логов
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var errs []error
	if err := l.appFile.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := l.errorFile.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing log files: %v", errs)
	}
	return nil
}

// Printf пишет форматированное сообщение в app.log
func (l *Logger) Printf(format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infoLogger.Printf(format, v...)
}

// Print пишет сообщение в app.log
func (l *Logger) Print(v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infoLogger.Print(v...)
}

// Println пишет сообщение с новой строкой в app.log
func (l *Logger) Println(v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infoLogger.Println(v...)
}

// Errorf пишет форматированное сообщение об ошибке в error.log
func (l *Logger) Errorf(format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorLogger.Printf(format, v...)
}

// Error пишет сообщение об ошибке в error.log
func (l *Logger) Error(v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorLogger.Print(v...)
}

// Errorln пишет сообщение об ошибке с новой строкой в error.log
func (l *Logger) Errorln(v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorLogger.Println(v...)
}

// Fatal пишет сообщение об ошибке в error.log и завершает программу
func (l *Logger) Fatal(v ...interface{}) {
	l.mu.Lock()
	l.errorLogger.Print(v...)
	l.mu.Unlock()
	os.Exit(1)
}

// Fatalf пишет форматированное сообщение об ошибке в error.log и завершает программу
func (l *Logger) Fatalf(format string, v ...interface{}) {
	l.mu.Lock()
	l.errorLogger.Printf(format, v...)
	l.mu.Unlock()
	os.Exit(1)
}

// Fatalln пишет сообщение об ошибке с новой строкой в error.log и завершает программу
func (l *Logger) Fatalln(v ...interface{}) {
	l.mu.Lock()
	l.errorLogger.Println(v...)
	l.mu.Unlock()
	os.Exit(1)
}

// Writer возвращает io.Writer для записи в app.log
func (l *Logger) Writer() io.Writer {
	return l.infoLogger.Writer()
}
