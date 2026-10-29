package controljson

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isPlatformRoutineDisconnect(err error) bool {
	return errors.Is(err, windows.ERROR_NO_DATA)
}
