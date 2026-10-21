package store

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type APKInfo struct {
	Package, VersionName, Certificate string
	VersionCode                       int64
	MinSDK                            int
}

var packageRE = regexp.MustCompile(`package: name='([^']+)' versionCode='([0-9]+)' versionName='([^']*)'`)
var sdkRE = regexp.MustCompile(`(?:minSdkVersion|sdkVersion):'([0-9]+)'`)
var certRE = regexp.MustCompile(`(?m)^Signer #[0-9]+ certificate SHA-256 digest: ([a-fA-F0-9]+)\r?$`)

func (s *Server) inspectAPK(ctx context.Context, path string) (APKInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	aapt, signer := s.cfg.AAPT, s.cfg.APKSigner
	if aapt == "" {
		aapt = "aapt"
	}
	if signer == "" {
		signer = "apksigner"
	}
	cert, err := exec.CommandContext(ctx, signer, "verify", "--verbose", "--print-certs", path).CombinedOutput()
	if err != nil {
		return APKInfo{}, fmt.Errorf("signature verification: %w", err)
	}
	certs := certRE.FindAllStringSubmatch(string(cert), -1)
	if len(certs) != 1 {
		return APKInfo{}, fmt.Errorf("exactly one APK signer is required")
	}
	// Reject v1-only APKs: the manifest and contents must be protected by v2 or v3 signing.
	if !strings.Contains(string(cert), "Verified using v2 scheme (APK Signature Scheme v2): true") && !strings.Contains(string(cert), "Verified using v3 scheme (APK Signature Scheme v3): true") {
		return APKInfo{}, fmt.Errorf("APK must use v2 or v3 signing")
	}
	meta, err := exec.CommandContext(ctx, aapt, "dump", "badging", path).Output()
	if err != nil {
		return APKInfo{}, fmt.Errorf("read APK metadata: %w", err)
	}
	p := packageRE.FindStringSubmatch(string(meta))
	sdk := sdkRE.FindStringSubmatch(string(meta))
	if len(p) != 4 || len(sdk) != 2 {
		return APKInfo{}, fmt.Errorf("invalid package metadata")
	}
	version, err := strconv.ParseInt(p[2], 10, 64)
	if err != nil {
		return APKInfo{}, err
	}
	min, err := strconv.Atoi(sdk[1])
	if err != nil {
		return APKInfo{}, err
	}
	return APKInfo{Package: p[1], VersionName: p[3], VersionCode: version, MinSDK: min, Certificate: strings.ToLower(certs[0][1])}, nil
}
