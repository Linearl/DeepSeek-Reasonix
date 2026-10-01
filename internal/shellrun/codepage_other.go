//go:build !windows

package shellrun

import "golang.org/x/text/encoding"

// platformDecoderCandidates is empty off Windows: POSIX shells and their tools
// speak UTF-8, so raw output passes through unchanged (decodeConsoleOutput
// still guards with the strict UTF-8 check first).
func platformDecoderCandidates() []encoding.Encoding { return nil }
