package pathpin

import "fmt"

// ifNameSize is IFNAMSIZ. nft oifname compares a 16-byte field.
const ifNameSize = 16

const warpAllowPrefix = "genesis-default-iface:"

// warpAllowUserData tags the accept rule Genesis inserts into WARP's filter.
func warpAllowUserData(iface string) []byte {
	return []byte(warpAllowPrefix + iface)
}

// oifNameData pads an interface name for an nft oifname comparison.
func oifNameData(iface string) ([]byte, error) {
	if iface == "" || len(iface) >= ifNameSize {
		return nil, fmt.Errorf("pathpin: interface name %q does not fit oifname", iface)
	}
	buf := make([]byte, ifNameSize)
	copy(buf, iface)
	return buf, nil
}
