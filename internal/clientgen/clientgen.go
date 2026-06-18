// Package clientgen builds a complete client proxy config (sing-box or xray)
// from a parsed proxyuri.Proxy, so any subscription proxy can be tested by
// standing up a local SOCKS inbound that egresses through it.
package clientgen

import (
	"errors"

	"github.com/hiddify/hiddify_config_health/internal/proxyuri"
)

// ErrUnsupported is returned when a core cannot express a given protocol or
// transport, so the caller can mark that core/proxy combo as Supported=false
// rather than treating it as a failure.
var ErrUnsupported = errors.New("unsupported protocol/transport for this core")

// jsonObj is a convenience alias for building configs.
type jsonObj = map[string]any

// Generate returns the client config JSON for the given core ("sing-box" or
// "xray"), proxy, and local SOCKS port.
func Generate(core string, p proxyuri.Proxy, socksPort int) ([]byte, error) {
	switch core {
	case "sing-box":
		return SingboxClient(p, socksPort)
	case "xray":
		return XrayClient(p, socksPort)
	default:
		return nil, ErrUnsupported
	}
}
