package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"registry-mirror/internal/secret"
)

func main() {
	var key string
	var decrypt bool
	flag.StringVar(&key, "key", os.Getenv("REGISTRY_MIRROR_CONFIG_KEY"), "encryption key; defaults to REGISTRY_MIRROR_CONFIG_KEY")
	flag.BoolVar(&decrypt, "decrypt", false, "decrypt instead of encrypt")
	flag.Parse()

	input := strings.Join(flag.Args(), " ")
	if input == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		input = strings.TrimRight(string(b), "\r\n")
	}
	if input == "" {
		fmt.Fprintln(os.Stderr, "plaintext or ciphertext is required")
		os.Exit(1)
	}

	var (
		out string
		err error
	)
	if decrypt {
		out, err = secret.Decrypt(input, key)
	} else {
		out, err = secret.Encrypt(input, key)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(out)
}
