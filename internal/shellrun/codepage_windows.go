//go:build windows

package shellrun

import (
	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// platformDecoderCandidates lists the Windows system decoders to try, most
// likely first:
//  1. the active console output code page — what cmd.exe and classic console
//     programs actually write (defaults to the OEM code page, `chcp` can move
//     it);
//  2. the ANSI code page — for programs that render via the ANSI subset;
//  3. GB18030 when the system is Chinese (see chineseSupersetDecoder).
func platformDecoderCandidates() []encoding.Encoding {
	var cps []uint32
	if cp, err := windows.GetConsoleOutputCP(); err == nil && cp != 0 {
		cps = append(cps, cp)
	}
	if cp := windows.GetACP(); cp != 0 {
		cps = append(cps, cp)
	}
	seen := make(map[uint32]bool, len(cps))
	var out []encoding.Encoding
	for _, cp := range cps {
		if seen[cp] {
			continue
		}
		seen[cp] = true
		if enc, ok := encodingForCodePage(cp); ok {
			out = append(out, enc)
		}
	}
	if sup := chineseSupersetDecoder(cps); sup != nil {
		out = append(out, sup)
	}
	return out
}

// encodingForCodePage maps the Windows code page identifiers that x/text can
// decode faithfully. Unknown code pages are skipped rather than guessed.
func encodingForCodePage(cp uint32) (encoding.Encoding, bool) {
	switch cp {
	case 437:
		return charmap.CodePage437, true
	case 850:
		return charmap.CodePage850, true
	case 866:
		return charmap.CodePage866, true
	case 874:
		return charmap.Windows874, true
	case 932:
		return japanese.ShiftJIS, true
	case 936:
		return simplifiedchinese.GBK, true
	case 949:
		return korean.EUCKR, true
	case 950:
		return traditionalchinese.Big5, true
	case 1250:
		return charmap.Windows1250, true
	case 1251:
		return charmap.Windows1251, true
	case 1252:
		return charmap.Windows1252, true
	case 1253:
		return charmap.Windows1253, true
	case 1254:
		return charmap.Windows1254, true
	case 1255:
		return charmap.Windows1255, true
	case 1256:
		return charmap.Windows1256, true
	case 1257:
		return charmap.Windows1257, true
	case 1258:
		return charmap.Windows1258, true
	default:
		return nil, false
	}
}
