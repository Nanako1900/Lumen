// Package sfu is the Pion selective-forwarding unit (contract §4.6,
// server-design §4). Each voice channel maps to an in-memory Room whose members
// each hold one server-side PeerConnection. The server is the offerer; audio
// Opus RTP is forwarded without transcoding. State is in-memory and cleared on
// restart.
//
// sfu does not import signaling (to avoid an import cycle). It notifies the
// signaling layer through an injected RoomEventSink and sends offers/ICE via
// per-member send closures.
package sfu

import (
	"fmt"
	"strings"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// NewAPI builds the shared, container/VPS-friendly *webrtc.API: a single UDP
// port (UDPMux), public-IP host candidates (1:1 NAT), and mDNS disabled
// (contract §4.6, research 01). It MUST be created before any PeerConnection;
// all PeerConnections share the returned API.
func NewAPI(udpPort int, publicIP string) (*webrtc.API, error) {
	se := webrtc.SettingEngine{}

	// (a) Single UDP port mux across all interfaces (container-equivalent to
	//     binding 0.0.0.0:udpPort/udp), excluding virtual interfaces.
	mux, err := ice.NewMultiUDPMuxFromPort(udpPort,
		ice.UDPMuxFromPortWithInterfaceFilter(func(name string) bool {
			return !strings.Contains(name, "docker") &&
				!strings.Contains(name, "veth") &&
				!strings.HasPrefix(name, "br-")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 UDPMux 失败: %w", err)
	}
	se.SetICEUDPMux(mux)

	// (b) Replace host candidates with the public IP (1:1 NAT). Without this,
	//     candidates carry the Docker-internal IP and clients cannot connect.
	if publicIP != "" {
		se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
	}

	// (c) Disable mDNS inside containers to avoid .local candidates.
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)

	return webrtc.NewAPI(webrtc.WithSettingEngine(se)), nil
}
