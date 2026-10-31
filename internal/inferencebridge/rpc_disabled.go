//go:build !kanarpc_stream

package inferencebridge

import "errors"

func New(Config) (Client, error) {
	return nil, errors.New("streaming inference requires kanarpc_stream and the reviewed local RPC SDK; no alternative provider was called")
}
