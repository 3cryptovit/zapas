package stock_test

import (
	"os"
	"testing"

	"github.com/vostapenko/zapas/internal/testsupport"
)

// База поднимается один раз на пакет; изоляцию внутри дают тенанты.
func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }
