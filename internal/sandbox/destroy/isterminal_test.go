package destroy

import (
	"os"
	"strings"
	"testing"
)

func TestIsTerminalIsFalseForEverythingButATerminal(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()

	if isTerminal(devNull) {
		t.Error("/dev/null is a terminal")
	}
	if isTerminal(reader) {
		t.Error("a pipe is a terminal")
	}
	if isTerminal(strings.NewReader("y\n")) {
		t.Error("a string reader is a terminal")
	}
}
