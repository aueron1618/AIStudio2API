package chromeauth

import (
	"bytes"
	"slices"
	"testing"

	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
)

func TestChrome157ProfilePreservesProtocolShape(t *testing.T) {
	base, err := profiles.Chrome_146.GetClientHelloSpec()
	if err != nil {
		t.Fatal(err)
	}
	var baseSignatures []tls.SignatureScheme
	for _, extension := range base.Extensions {
		if signatures, ok := extension.(*tls.SignatureAlgorithmsExtension); ok {
			baseSignatures = slices.Clone(signatures.SupportedSignatureAlgorithms)
		}
	}
	for _, priority := range []chromePriority{chromeIdle, chromeHighest} {
		profile := chrome157Profile(priority)
		if profile.GetClientHelloId().Version != "157" || profile.GetHeaderPriority().Weight != priority.weight || !profile.GetHeaderPriority().Exclusive {
			t.Fatal("Chrome version or HTTP/2 priority lost")
		}
		if !slices.Equal(profile.GetSettingsOrder(), profiles.Chrome_146.GetSettingsOrder()) || !slices.Equal(profile.GetPseudoHeaderOrder(), profiles.Chrome_146.GetPseudoHeaderOrder()) {
			t.Fatal("Chrome HTTP/2 ordering changed")
		}
		for range 2 {
			spec, err := profile.GetClientHelloSpec()
			if err != nil {
				t.Fatal(err)
			}
			signatures, anchors := false, false
			for _, extension := range spec.Extensions {
				switch extension := extension.(type) {
				case *tls.SignatureAlgorithmsExtension:
					signatures = true
					algorithms := extension.SupportedSignatureAlgorithms
					if len(algorithms) != len(baseSignatures)+4 || algorithms[0]&0x0f0f != 0x0a0a || !slices.Equal(algorithms[1:4], []tls.SignatureScheme{0x0904, 0x0905, 0x0906}) || !slices.Equal(algorithms[4:], baseSignatures) {
						t.Fatalf("invalid Chrome 157 signature algorithms: %x", algorithms)
					}
				case *tls.GenericExtension:
					if extension.Id == 0xca34 {
						anchors = bytes.Equal(extension.Data, chromeTrustAnchors)
					}
				}
			}
			if !signatures || !anchors {
				t.Fatal("Chrome TLS extensions missing")
			}
		}
	}
}
