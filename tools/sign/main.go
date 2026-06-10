// Command sign provides two subcommands for managing beckn-constants signing.
//
// Usage:
//
//	sign gen-key --priv <file> --pub <file>   generate an Ed25519 keypair
//	sign sign    --key <file> --input <file> --output <file>   sign a file
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sign <gen-key|sign> [flags]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "gen-key":
		runGenKey(os.Args[2:])
	case "sign":
		runSign(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q — expected gen-key or sign\n", os.Args[1])
		os.Exit(1)
	}
}

func runGenKey(args []string) {
	fs := flag.NewFlagSet("gen-key", flag.ExitOnError)
	privOut := fs.String("priv", "beckn_private.key", "output file for private key seed (base64, keep secret)")
	pubOut := fs.String("pub", "beckn_public_key.pem", "output file for public key (PEM)")
	fs.Parse(args)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatalf("generate key: %v", err)
	}

	privData := base64.StdEncoding.EncodeToString(priv.Seed())
	if err := os.WriteFile(*privOut, []byte(privData), 0600); err != nil {
		fatalf("write private key: %v", err)
	}

	pkix, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		fatalf("marshal public key: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkix})
	if err := os.WriteFile(*pubOut, pemData, 0644); err != nil {
		fatalf("write public key: %v", err)
	}

	fmt.Printf("keypair generated\n  public key  → %s  (commit this)\n  private key → %s  (store in CI secret, never commit)\n", *pubOut, *privOut)
}

func runSign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyFile := fs.String("key", "", "private key file (base64-encoded seed)")
	inputFile := fs.String("input", "", "file to sign")
	outputFile := fs.String("output", "", "output .sig file")
	fs.Parse(args)

	if *keyFile == "" || *inputFile == "" || *outputFile == "" {
		fmt.Fprintln(os.Stderr, "sign: --key, --input, and --output are all required")
		os.Exit(1)
	}

	keyData, err := os.ReadFile(*keyFile)
	if err != nil {
		fatalf("read key file: %v", err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyData)))
	if err != nil {
		fatalf("decode private key: %v", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	content, err := os.ReadFile(*inputFile)
	if err != nil {
		fatalf("read input: %v", err)
	}

	sig := ed25519.Sign(priv, content)
	encoded := base64.StdEncoding.EncodeToString(sig)
	if err := os.WriteFile(*outputFile, []byte(encoded), 0644); err != nil {
		fatalf("write signature: %v", err)
	}

	fmt.Printf("signed %s → %s\n", *inputFile, *outputFile)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
