package main

import (
	"context"
	"fmt"
	plugins "plugins/plugin"
)

func main() {
	pluginManager, err := plugins.New("./plugin/implementations", "configs/plugin.yaml")
	if err != nil {
		fmt.Println("Error initializing plugin manager:", err)
		return
	}
	defer pluginManager.Close()

	signer, err := pluginManager.GetSigner()
	if err != nil {
		fmt.Println("Error loading signer plugin:", err)
		return
	}

	signature, err := signer.Sign(context.Background(), []byte("test payload"), "key123")
	if err != nil {
		fmt.Println("Error signing data:", err)
		return
	}
	fmt.Println("Generated Signature:", signature)

	verifier, err := pluginManager.GetVerifier()
	if err != nil {
		fmt.Println("Error loading verifier plugin:", err)
		return
	}

	// headerString := `Authorization: Signature keyId="sub123|ukid456|ed25519",algorithm="ed25519", created="1740477430", expires="1740481030", headers="(created) (expires) digest", signature="z6OARtt7BiPr7/4jIULblMAkLdwf9JSicyfM9toa2y4MDSnuhQzJg62EdN4Zl42dfISqzJo/XnFywCW55pBOAg=="`

	headerString := "w2FBqUYJrJUmwBkLM/mjFk3/eT7Sal6xI1TjoUxJwtQQEc3mze8djnbZhQGUXCv1kPTYneM2F+07vogXt1VxBA=="

	valid, err := verifier.Verify(context.Background(), []byte("test payload"), []byte(headerString))
	if err != nil {
		fmt.Println("Error verifying signature:", err)
		return
	}
	fmt.Println("Signature Valid:", valid)
}
