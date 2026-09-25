package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const assetsLabel = "eredo.assets"

// assetsHash fingerprints the image sources embedded in this binary. Images
// carry it as a label, so a binary upgrade that changes the proxy config, the
// hooks or a Dockerfile rebuilds the images instead of silently running old
// ones.
func assetsHash() string {
	var paths []string
	for _, root := range []string{"proxy", "sandbox"} {
		_ = fs.WalkDir(assets, root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				paths = append(paths, p)
			}
			return nil
		})
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		b, _ := assets.ReadFile(p)
		h.Write([]byte(p + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// cmdBuild builds both images from the sources embedded in the binary.
func cmdBuild(pull bool) error {
	tmp, err := os.MkdirTemp("", "eredo-build")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	sandboxImg, proxyImg := imageNames()
	for _, d := range []string{"proxy", "sandbox"} {
		if err := writeEmbeddedDir(d, filepath.Join(tmp, d)); err != nil {
			return err
		}
	}
	common := []string{"build", "--label", assetsLabel + "=" + assetsHash()}
	if pull {
		common = append(common, "--pull")
	}
	info("Building %s", proxyImg)
	if err := dockerTTY(append(append([]string{}, common...), "-t", proxyImg, filepath.Join(tmp, "proxy"))...); err != nil {
		return err
	}
	info("Building %s", sandboxImg)
	return dockerTTY(append(append([]string{}, common...), "--build-arg", "TZ="+hostTZ(), "-t", sandboxImg, filepath.Join(tmp, "sandbox"))...)
}

// imageCurrent reports whether an image exists and was built from this
// binary's sources. Custom images (EREDO_IMAGE, EREDO_PROXY_IMAGE) are
// trusted as they are.
func imageCurrent(img, def string) bool {
	if !dockerOK("image", "inspect", img) {
		return false
	}
	if img != def {
		return true
	}
	return dockerOut("image", "inspect", "-f", `{{index .Config.Labels "`+assetsLabel+`"}}`, img) == assetsHash()
}

func imagesCurrent() bool {
	sandboxImg, proxyImg := imageNames()
	return imageCurrent(sandboxImg, sandboxImage) && imageCurrent(proxyImg, proxyImage)
}

func ensureImages() error {
	if imagesCurrent() {
		return nil
	}
	sandboxImg, proxyImg := imageNames()
	if dockerOK("image", "inspect", sandboxImg) && dockerOK("image", "inspect", proxyImg) {
		info("images were built by another eredo version; rebuilding")
	}
	return cmdBuild(false)
}

// imageIDs identifies the exact images a sandbox runs on; it goes into the
// sandbox spec so a rebuilt image recreates the sandbox on its next `up`.
func imageIDs() string {
	sandboxImg, proxyImg := imageNames()
	short := func(id string) string {
		if len(id) > 19 {
			return id[:19]
		}
		return id
	}
	return short(dockerOut("image", "inspect", "-f", "{{.Id}}", sandboxImg)) + "," + short(dockerOut("image", "inspect", "-f", "{{.Id}}", proxyImg))
}

// cmdUpdate pulls fresh base images and rebuilds, which also installs the
// newest Claude Code. Sandboxes switch to the new images on their next `up`;
// their config and history volumes are kept.
func cmdUpdate() error {
	if err := ensureDocker(); err != nil {
		return err
	}
	if err := cmdBuild(true); err != nil {
		return err
	}
	info("images rebuilt; each sandbox is recreated on its next `eredo up` (config and history kept)")
	return nil
}
