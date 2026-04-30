package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultMaxSize    = 10 * 1024 * 1024 // 10 MB
	defaultMaxBackups = 3
)

// rotatingWriter пишет в файл и автоматически ротирует при превышении maxSize
type rotatingWriter struct {
	filename   string
	maxSize    int64
	maxBackups int
	size       int64
	file       *os.File
	mu         sync.Mutex
}

func newRotatingWriter(filename string, maxSize int64, maxBackups int) (*rotatingWriter, error) {
	w := &rotatingWriter{
		filename:   filename,
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}
	info, err := os.Stat(filename)
	if err == nil {
		w.size = info.Size()
	}
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	w.file = f
	return w, nil
}

func (w *rotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func (w *rotatingWriter) rotate() error {
	w.file.Close()

	// Shift backups: .2 -> .3, .1 -> .2
	for i := w.maxBackups - 1; i > 0; i-- {
		oldPath := fmt.Sprintf("%s.%d", w.filename, i)
		newPath := fmt.Sprintf("%s.%d", w.filename, i+1)
		os.Remove(newPath) // Remove oldest if exists
		os.Rename(oldPath, newPath)
	}
	os.Remove(w.filename + ".1")
	os.Rename(w.filename, w.filename+".1")

	f, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	return nil
}

// Logger - кастомный логгер с поддержкой записи в файлы и ротацией
type Logger struct {
	infoLogger  *log.Logger
	errorLogger *log.Logger
	logDir      string
	appWriter   *rotatingWriter
	errorWriter *rotatingWriter
	mu          sync.Mutex
}

// New создает новый логгер с указанной директорией для логов
func New(logDir string, maxSizeMB, maxBackups int) (*Logger, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = defaultMaxSize / (1024 * 1024)
	}
	if maxBackups < 0 {
		maxBackups = defaultMaxBackups
	}
	maxSize := int64(maxSizeMB) * 1024 * 1024

	// Создаем директорию для логов если её нет
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Открываем файлы для логов с ротацией
	appWriter, err := newRotatingWriter(
		filepath.Join(logDir, "app.log"),
		maxSize,
		maxBackups,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open app.log: %w", err)
	}

	errorWriter, err := newRotatingWriter(
		filepath.Join(logDir, "error.log"),
		maxSize,
		maxBackups,
	)
	if err != nil {
		appWriter.Close()
		return nil, fmt.Errorf("failed to open error.log: %w", err)
	}

	// Создаем MultiWriter для дублирования вывода в консоль и файл
	appOut := io.MultiWriter(os.Stdout, appWriter)
	errorOut := io.MultiWriter(os.Stderr, errorWriter)

	logger := &Logger{
		infoLogger:  log.New(appOut, "", log.LstdFlags),
		errorLogger: log.New(errorOut, "ERROR: ", log.LstdFlags|log.Lshortfile),
		logDir:      logDir,
		appWriter:   appWriter,
		errorWriter: errorWriter,
	}

	logger.Printf("Logger initialized (max size: %d MB, max backups: %d)", maxSizeMB, maxBackups)
	return logger, nil
}

// Close закрывает файлы логов
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var errs []error
	if err := l.appWriter.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := l.errorWriter.Close(); err != nil {
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

// Writer возвращает io.Writer для записи в app.log
func (l *Logger) Writer() io.Writer {
	return l.infoLogger.Writer()
}
