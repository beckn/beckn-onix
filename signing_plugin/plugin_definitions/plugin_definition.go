package plugindefinitions

type SignatureAndValidation interface {

	// SignRequest generates a digital signature for the message
	//return the signature with this function
	// this should not accept http request only body acdpeted
	// need to verify whole request or marshal struck or byte array
	Sign(body []byte, subscriberId string, keyId string) (string, error)

	Verify(body []byte, header []byte) (bool, error)

	PublicKey() ([]byte, error)

	PrivateKey() ([]byte, error)
}
