package tests

import (
	"os"
	"testing"

	tests "github.com/beyondstorage/go-integration-test/v4"
)

func TestStorage(t *testing.T) {
	if os.Getenv("STORAGE_AZFILE_INTEGRATION_TEST") != "on" {
		t.Skipf("STORAGE_AZFILE_INTEGRATION_TEST is not 'on', skipped")
	}
	tests.TestStorager(t, setupTest(t))
}

func TestDir(t *testing.T) {
	if os.Getenv("STORAGE_AZFILE_INTEGRATION_TEST") != "on" {
		t.Skipf("STORAGE_AZFILE_INTEGRATION_TEST is not 'on', skipped")
	}
	tests.TestDirer(t, setupTest(t))
}