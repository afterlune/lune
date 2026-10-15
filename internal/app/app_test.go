package app

import (
	"os"
	"testing"
)

func TestUseTUIRequiresBothStreamsToBeTerminals(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	if useTUI(input, output) {
		t.Fatal("piped streams should use the line interface")
	}
}
