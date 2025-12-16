package cmd

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

// Gets SC variable details
func GetSCVariables(keysstring map[string]any, keysuint64 map[uint64]any) (variables []*structures.SCIDVariable) {
	//balances = make(map[string]uint64)

	isAlpha := regexp.MustCompile(`^[A-Za-z]+$`).MatchString

	for k, v := range keysstring {
		contractVar := &structures.SCIDVariable{}
		contractVar.Key = k
		switch cval := v.(type) {
		case float64:
			contractVar.Value = uint64(cval)
		case uint64:
			contractVar.Value = cval
		case string:
			// hex decode since all strings are hex encoded
			dstr, _ := hex.DecodeString(cval)
			// Check if dstr is an address raw
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				contractVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(dstr)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							contractVar.Value = str
						} else {
							contractVar.Value = h.String()
						}
					} else {
						contractVar.Value = str
					}
				} else {
					contractVar.Value = str
				}
			}
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						contractVar.Value = str
					} else {
						contractVar.Value = h.String()
					}
				} else {
					contractVar.Value = str
				}
			} else {
				contractVar.Value = str
			}
		}
		variables = append(variables, contractVar)
	}

	for k, v := range keysuint64 {
		contractVar := &structures.SCIDVariable{}
		contractVar.Key = k
		switch cval := v.(type) {
		case string:
			// hex decode since all strings are hex encoded
			decd, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(decd); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				contractVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(decd)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							contractVar.Value = str
						} else {
							contractVar.Value = h.String()
						}
					} else {
						contractVar.Value = str
					}
				} else {
					contractVar.Value = str
				}
			}
		case uint64:
			contractVar.Value = cval
		case float64:
			contractVar.Value = uint64(cval)
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						contractVar.Value = str
					} else {
						contractVar.Value = h.String()
					}
				} else {
					contractVar.Value = str
				}
			} else {
				contractVar.Value = str
			}
		}
		variables = append(variables, contractVar)
	}

	return variables
}

func GetSCNameFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		key := strings.ToLower(k)
		if !strings.Contains(key, "name") &&
			(!strings.HasPrefix(key, "name") || !strings.HasSuffix(key, "name")) {
			// key != "name" || //explicit check
			// infer a name

			continue
		}

		str, ok := v.(string)
		if !ok {
			continue
		}
		b, e := hex.DecodeString(str)
		if e != nil {
			continue // what else can we do ?
		}
		text = string(b)
	}
	if text == "" {
		return "null"
	}
	return text
}
func GetSCDescriptionFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {

		if !strings.Contains(k, "descr") && !strings.HasPrefix(k, "descr") {
			continue
		}
		value, ok := v.(string)
		if !ok {
			continue
		}
		b, e := hex.DecodeString(value)
		if e != nil {
			continue // what else can we do ?
		}

		text = string(b)
	}

	if text == "" {
		return "null"
	}
	return text
}

func GetSCIDImageURLFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "icon") && !strings.HasPrefix(k, "icon") {
			continue
		}
		value, ok := v.(string)
		if !ok {
			continue
		}
		b, e := hex.DecodeString(value)
		if e != nil {
			continue // what else can we do ?
		}

		text = string(b)
	}
	if text == "" {
		return "null"
	}
	u, err := url.Parse(text)
	if err != nil {
		return "null"
	}
	return u.String()
}
func GetBlockDeserialized(blob string) block.Block {

	var bl block.Block
	b, err := hex.DecodeString(blob)
	if err != nil {
		// should probably log or handle this error
		fmt.Println(err.Error())
		return block.Block{}
	}
	bl.Deserialize(b)
	return bl
}

func GetSCHeaderFromMetaData(kv map[string]any) string {

	name, description, image := "null", "null", "null"

	for key, value := range kv {

		k := strings.ToLower(key)
		if !strings.Contains(k, "metadata") || key == "metadataFormat" {
			continue
		}

		v, ok := value.(string)
		if !ok {
			continue
		}

		b, e := hex.DecodeString(v)
		if e != nil {
			continue // what else can we do ?
		}

		var metadata map[string]any
		if err := json.Unmarshal(b, &metadata); err != nil {
			fmt.Println(err, k, string(b))
			continue
		}

		v, ok = metadata["name"].(string)
		if !ok {
			name = "null"
		} else {
			name = v
		}

		b, e = json.Marshal(metadata["attributes"])
		if e != nil {
			description = "null"
		} else {
			description = string(b)
		}

		v, ok = metadata["image"].(string)
		if !ok {
			image = "null"
		}
		u, err := url.Parse(v)
		if err != nil {
			image = "null"
		} else {
			image = u.String()
		}

	}

	return name + ";" + description + ";" + image
}
