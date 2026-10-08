//go:build !(linux && (amd64 || arm64))

package pick

import "errors"

// showFailure is Linux-only for now: elsewhere the message is not shown.
func showFailure(string) error {
	return errors.New("showing a failure on the terminal is not supported on this platform")
}
