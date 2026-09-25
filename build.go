package main

import (
	"os"
	"path/filepath"
)

// cmdBuild builds both images from the sources embedded in the binary.
func cmdBuild(pull bool) error {
	tmp, err := os.MkdirTemp("", "moat-build")
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
	pullArg := []string{}
	if pull {
		pullArg = []string{"--pull"}
	}
	info("Building %s", proxyImg)
	args := append([]string{"build"}, pullArg...)
	if err := dockerTTY(append(args, "-t", proxyImg, filepath.Join(tmp, "proxy"))...); err != nil {
		return err
	}
	info("Building %s", sandboxImg)
	args = append([]string{"build"}, pullArg...)
	return dockerTTY(append(args, "--build-arg", "TZ="+hostTZ(), "-t", sandboxImg, filepath.Join(tmp, "sandbox"))...)
}

func ensureImages() error {
	sandboxImg, proxyImg := imageNames()
	if !dockerOK("image", "inspect", sandboxImg) || !dockerOK("image", "inspect", proxyImg) {
		return cmdBuild(false)
	}
	return nil
}
