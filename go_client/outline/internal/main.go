package internal

import (
	"C"
	"io"
	"log"
	"os"
)

var logging = &struct {
	Debug, Info, Warn, Err *log.Logger
}{
	Debug: log.New(io.Discard, "[DEBUG] ", log.LstdFlags),
	Info:  log.New(os.Stdout, "[INFO] ", log.LstdFlags),
	Warn:  log.New(os.Stderr, "[WARN] ", log.LstdFlags),
	Err:   log.New(os.Stderr, "[ERROR] ", log.LstdFlags),
}

// Инициализация логирования в файл
func initLogToFile() (*os.File, error) {
	file, err := os.OpenFile("logs.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Перенаправление стандартного вывода и ошибок в файл
func setupLogging(file *os.File) {
	// Перенаправляем стандартный вывод и ошибки
	//os.Stdout = file
	//os.Stderr = file

	// Настроить логгер для записи в файл
	logging.Debug.SetOutput(file)
	logging.Info.SetOutput(file)
	logging.Warn.SetOutput(file)
	logging.Err.SetOutput(file)
	log.SetOutput(file)
}
