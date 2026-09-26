package negotiate

import "strings"

// VarySet is the set of request headers a response depends on
// (ARCHITECTURE §9.1 Vary row). It is a bit set so the negotiation and the
// client-hint resolution can be combined with | and rendered once; it never
// contains "*".
type VarySet uint16

// Vary members, in the order Header renders them.
const (
	VaryAccept VarySet = 1 << iota
	VarySecCHDPR
	VaryDPR
	VarySecCHWidth
	VaryWidth
	VarySecCHViewportWidth
	VaryViewportWidth
	VarySaveData
	VaryECT

	varyEnd
)

// Header-group shorthands: a hint consulted by any of its names varies on all
// of them, because either name can change the result.
const (
	varyDPRGroup      = VarySecCHDPR | VaryDPR
	varyWidthGroup    = VarySecCHWidth | VaryWidth | VarySecCHViewportWidth | VaryViewportWidth
	varySaveDataGroup = VarySaveData | VaryECT
)

// varyNames returns the canonical header name of a single member.
func varyNames(v VarySet) string {
	switch v {
	case VaryAccept:
		return "Accept"
	case VarySecCHDPR:
		return "Sec-CH-DPR"
	case VaryDPR:
		return "DPR"
	case VarySecCHWidth:
		return "Sec-CH-Width"
	case VaryWidth:
		return "Width"
	case VarySecCHViewportWidth:
		return "Sec-CH-Viewport-Width"
	case VaryViewportWidth:
		return "Viewport-Width"
	case VarySaveData:
		return "Save-Data"
	case VaryECT:
		return headerECT
	default:
		return ""
	}
}

// Has reports whether every member of m is in v.
func (v VarySet) Has(m VarySet) bool { return v&m == m }

// Names returns the header names in v in a stable order.
func (v VarySet) Names() []string {
	var out []string
	for m := VarySet(1); m < varyEnd; m <<= 1 {
		if v&m != 0 {
			out = append(out, varyNames(m))
		}
	}
	return out
}

// Header returns the Vary field value ("" when v is empty). The common
// single-member case returns a constant.
func (v VarySet) Header() string {
	switch v {
	case 0:
		return ""
	case VaryAccept:
		return "Accept"
	default:
		return strings.Join(v.Names(), ", ")
	}
}

// String implements fmt.Stringer; it equals Header.
func (v VarySet) String() string { return v.Header() }
