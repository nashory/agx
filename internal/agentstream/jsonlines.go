package agentstream

import (
	"bufio"
	"errors"
	"io"
)

// ReadJSONLines reads newline-delimited events without bufio.Scanner's token
// size limit. The final line is delivered even when the stream omits a newline.
func ReadJSONLines(r io.Reader, handle func([]byte) error) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if handleErr := handle(line); handleErr != nil {
				return handleErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
