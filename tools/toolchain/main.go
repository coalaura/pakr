package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coalaura/builder/goenv"
)

func main() {
	err := generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}
}

func generate() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: toolchain <os> <arch> <output directory>")
	}

	targetOS := os.Args[1]
	arch := os.Args[2]
	directory := os.Args[3]

	if !goenv.SupportsCGO(targetOS, arch) {
		return fmt.Errorf("unsupported target %s/%s", targetOS, arch)
	}

	config := goenv.Prepare(goenv.Options{OS: targetOS, Arch: arch, CGO: true})

	err := os.MkdirAll(directory, 0o755)
	if err != nil {
		return err
	}

	compilers := map[string]string{"cc": config.Env["CC"], "cxx": config.Env["CXX"], "ar": "zig ar", "ranlib": "zig ranlib"}

	for name, command := range compilers {
		content := "#!/usr/bin/env sh\nexec " + command + " \"$@\"\n"

		err = os.WriteFile(filepath.Join(directory, name), []byte(content), 0o755)
		if err != nil {
			return err
		}
	}

	systems := map[string]string{"linux": "Linux", "windows": "Windows", "darwin": "Darwin"}
	processors := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}

	var content strings.Builder

	fmt.Fprintf(&content, "set(CMAKE_SYSTEM_NAME %s)\nset(CMAKE_SYSTEM_PROCESSOR %s)\n", systems[targetOS], processors[arch])
	fmt.Fprintf(&content, "set(CMAKE_C_COMPILER \"%s/cc\")\nset(CMAKE_CXX_COMPILER \"%s/cxx\")\n", directory, directory)
	fmt.Fprintf(&content, "set(CMAKE_AR \"%s/ar\")\nset(CMAKE_RANLIB \"%s/ranlib\")\n", directory, directory)

	content.WriteString("set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)\nset(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)\nset(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)\nset(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE ONLY)\n")

	return os.WriteFile(filepath.Join(directory, "toolchain.cmake"), []byte(content.String()), 0o644)
}
