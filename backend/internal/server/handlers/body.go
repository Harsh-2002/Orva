package handlers

import (
	"errors"
	"io"
)

func readBoundedBody(body io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("request body exceeds limit")
	}
	return b, nil
}
