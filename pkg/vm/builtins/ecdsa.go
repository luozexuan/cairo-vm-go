package builtins

import (
	"fmt"
	"math/big"

	"github.com/NethermindEth/cairo-vm-go/pkg/utils"
	"github.com/NethermindEth/cairo-vm-go/pkg/vm/memory"
	starkcurve "github.com/consensys/gnark-crypto/ecc/stark-curve"
	ecdsa "github.com/consensys/gnark-crypto/ecc/stark-curve/ecdsa"
	"github.com/consensys/gnark-crypto/ecc/stark-curve/fp"
)

const (
	ECDSAName                  = "ecdsa"
	inputCellsPerECDSA         = 2
	cellsPerECDSA              = 2
	instancesPerComponentECDSA = 1
)

type ECDSA struct {
	Signatures  map[uint64]ecdsa.Signature
	ratio       uint64
	stopPointer uint64
}

// verify_ecdsa_signature(message_hash, public_key, sig_r, sig_s)
func (e *ECDSA) CheckWrite(segment *memory.Segment, offset uint64, value *memory.MemoryValue) error {
	ecdsaIndex := offset % cellsPerECDSA
	pubOffset := offset - ecdsaIndex
	msgOffset := pubOffset + 1

	pub := segment.Peek(pubOffset)
	msg := segment.Peek(msgOffset)

	//Both must be known to check the signature
	if !msg.Known() || !pub.Known() {
		return nil
	}

	pubX, err := pub.FieldElement() //X element of the sig
	if err != nil {
		return err
	}

	msgField, err := msg.FieldElement()
	if err != nil {
		return err
	}

	//Recover Y part of the public key
	posY, negY, err := recoverY(pubX)
	if err != nil {
		return err
	}

	//Try first with positive y
	key := starkcurve.G1Affine{X: *pubX, Y: posY}
	if !key.IsOnCurve() {
		return fmt.Errorf("key is not on curve")
	}

	pubKey := &ecdsa.PublicKey{A: key}
	sig, ok := e.Signatures[pubOffset]
	if !ok {
		return fmt.Errorf("signature is missing from ECDSA builtin")
	}

	msgBytes := msgField.Bytes()
	valid, err := pubKey.Verify(sig.Bytes(), msgBytes[:], nil)
	if err != nil {
		return err
	}

	if !valid {
		// Now try with Neg Y. Already know the point is on the curve so no need to check again
		key = starkcurve.G1Affine{X: *pubX, Y: negY}
		pubKey = &ecdsa.PublicKey{A: key}
		valid, err := pubKey.Verify(sig.Bytes(), msgBytes[:], nil)
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf("signature is not valid")
		}
	}
	return nil
}

func (e *ECDSA) InferValue(segment *memory.Segment, offset uint64) error {
	return fmt.Errorf("can't infer value")
}

/*
Hint that will call this function looks like this:

	"hints": {
	    "6": [
	        {
	            "accessible_scopes": [
	                "starkware.cairo.common.signature",
	                "starkware.cairo.common.signature.verify_ecdsa_signature"
	            ],
	            "code": "ecdsa_builtin.add_signature(ids.ecdsa_ptr.address_, (ids.signature_r, ids.signature_s))",
	            "flow_tracking_data": {
	                "ap_tracking": {
	                    "group": 2,
	                    "offset": 0
	                },
	                "reference_ids": {
	                    "starkware.cairo.common.signature.verify_ecdsa_signature.ecdsa_ptr": 4,
	                    "starkware.cairo.common.signature.verify_ecdsa_signature.message": 0,
	                    "starkware.cairo.common.signature.verify_ecdsa_signature.public_key": 1,
	                    "starkware.cairo.common.signature.verify_ecdsa_signature.signature_r": 2,
	                    "starkware.cairo.common.signature.verify_ecdsa_signature.signature_s": 3
	                }
	            }
	        }
	    ]
	},
*/
func (e *ECDSA) AddSignature(pubOffset uint64, r, s *fp.Element) error {
	if e.Signatures == nil {
		e.Signatures = make(map[uint64]ecdsa.Signature)
	}
	bytes := make([]byte, 0, 64)
	rBytes := r.Bytes()
	bytes = append(bytes, rBytes[:]...)
	sBytes := s.Bytes()
	bytes = append(bytes, sBytes[:]...)

	sig := ecdsa.Signature{}
	_, err := sig.SetBytes(bytes)
	if err != nil {
		return err
	}

	e.Signatures[pubOffset] = sig
	return nil
}

func (e *ECDSA) String() string {
	return ECDSAName
}

func (e *ECDSA) GetAllocatedSize(segmentUsedSize uint64, vmCurrentStep uint64) (uint64, error) {
	return getBuiltinAllocatedSize(segmentUsedSize, vmCurrentStep, e.ratio, inputCellsPerECDSA, instancesPerComponentECDSA, cellsPerECDSA)
}

// recoverY recovers the y and -y coordinate of x. True y can be either y or -y
func recoverY(x *fp.Element) (fp.Element, fp.Element, error) {
	// y_squared = (x * x * x + ALPHA * x + BETA) % FIELD_PRIME
	ax := &fp.Element{}
	ax.Mul(&utils.Alpha, x)
	x2 := &fp.Element{}
	x2.Mul(x, x)
	x2.Mul(x2, x)
	x2.Add(x2, ax)
	x2.Add(x2, &utils.Beta)
	y := x2.Sqrt(x2)
	if y == nil {
		return fp.Element{}, fp.Element{}, fmt.Errorf("invalid Public key")
	}
	negY := fp.Element{}
	negY.Neg(y)
	return *y, negY, nil
}

type AirPrivateBuiltinECDSASignatureInput struct {
	R string `json:"r"`
	W string `json:"w"`
}

type AirPrivateBuiltinECDSA struct {
	Index          int                                  `json:"index"`
	PubKey         string                               `json:"pubkey"`
	Msg            string                               `json:"msg"`
	SignatureInput AirPrivateBuiltinECDSASignatureInput `json:"signature_input"`
}

func (e *ECDSA) GetAirPrivateInput(ecdsaSegment *memory.Segment) ([]AirPrivateBuiltinECDSA, error) {
	values := make([]AirPrivateBuiltinECDSA, 0)
	for addrOffset, signature := range e.Signatures {
		idx := addrOffset / cellsPerECDSA
		pubKey, err := ecdsaSegment.Read(addrOffset)
		if err != nil {
			return values, err
		}
		msg, err := ecdsaSegment.Read(addrOffset + 1)
		if err != nil {
			return values, err
		}

		pubKeyBig := big.Int{}
		msgBig := big.Int{}
		pubKey.Felt.BigInt(&pubKeyBig)
		msg.Felt.BigInt(&msgBig)
		pubKeyHex := fmt.Sprintf("0x%x", &pubKeyBig)
		msgHex := fmt.Sprintf("0x%x", &msgBig)

		rBig := new(big.Int).SetBytes(signature.R[:])
		sBig := new(big.Int).SetBytes(signature.S[:])
		frModulusBig, _ := new(big.Int).SetString("3618502788666131213697322783095070105526743751716087489154079457884512865583", 10)
		wBig := new(big.Int).ModInverse(sBig, frModulusBig)
		signatureInput := AirPrivateBuiltinECDSASignatureInput{
			R: fmt.Sprintf("0x%x", rBig),
			W: fmt.Sprintf("0x%x", wBig),
		}

		values = append(values, AirPrivateBuiltinECDSA{Index: int(idx), PubKey: pubKeyHex, Msg: msgHex, SignatureInput: signatureInput})
	}
	return values, nil
}

func (e *ECDSA) GetCellsPerInstance() uint64 {
	return cellsPerECDSA
}

func (e *ECDSA) GetStopPointer() uint64 {
	return e.stopPointer
}

func (e *ECDSA) SetStopPointer(stopPointer uint64) {
	e.stopPointer = stopPointer
}
