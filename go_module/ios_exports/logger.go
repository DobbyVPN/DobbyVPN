package cloak_outline

import (
	"go_module/log"
)

func InitLogger(path string) {
	defer guard("InitLogger")()
	if err := log.SetPath(path); err != nil {
		log.Errorf(Category, "InitLogger failed: %v", err)
	}
}
