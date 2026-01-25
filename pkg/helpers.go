package pkg

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/bn256"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/secretnamebasis/simple-gnomon/db"
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
			// fmt.Println(err, k, string(b)) // just noise
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

func ValidateSCSignature(code string, key string) (validated bool, signer string, err error) {
	if key == "" {
		return
	}

	// CheckSignature
	filedata := []byte(key)
	p, _ := pem.Decode(filedata)
	if p == nil {
		fmt.Printf("[ValidateSCSignature] ERR - Unknown format of input data - %v\n", key)
		return
	}

	astr := p.Headers["Address"]
	cstr := p.Headers["C"]
	sstr := p.Headers["S"]

	addr, err := rpc.NewAddress(astr)
	if err != nil {
		fmt.Printf("[ValidateSCSignature] ERR - Cannot validate Address header\n")
		return
	}

	c, ok := new(big.Int).SetString(cstr, 16)
	if !ok {
		err = fmt.Errorf("[ValidateSCSignature] Unknown C format")
		return
	}

	s, ok := new(big.Int).SetString(sstr, 16)
	if !ok {
		err = fmt.Errorf("[ValidateSCSignature] Unknown S format")
		return
	}

	tmppoint := new(bn256.G1).Add(new(bn256.G1).ScalarMult(crypto.G, s), new(bn256.G1).ScalarMult(addr.PublicKey.G1(), new(big.Int).Neg(c)))
	serialize := fmt.Appendf(nil, "%s%s%x", addr.PublicKey.G1().String(), tmppoint.String(), p.Bytes)

	c_calculated := crypto.ReducedHash(serialize)
	if c.String() != c_calculated.String() {
		err = fmt.Errorf("[ValidateSCSignature] signature mismatch")
		return
	}

	signer = addr.String()
	message := p.Bytes

	if string(message) == code {
		validated = true
	}

	return
}

func gracefullyStop(cancel context.CancelFunc) {
	cancel()
	for database.Writing {
		fmt.Printf("waiting for dbs writer to stop\r")
		time.Sleep(time.Millisecond * 200)
	}
	fmt.Println()
	fmt.Println("gracefully stopped")
}
func GracefullyStopAndExit(cancel context.CancelFunc) {
	gracefullyStop(cancel)
	os.Exit(0)
}

func areQueuesEmpty() bool {
	return len(block_processing) != 0 ||
		len(transaction_processing) != 0 ||
		len(scid_processing) != 0 ||
		len(scid_db_queue) != 0 ||
		len(mini_db_queue) != 0 ||
		len(mini_queue) != 0
}
func longestQueue() int {
	return max(len(block_processing),
		len(transaction_processing),
		len(scid_processing),
		len(scid_db_queue),
		len(mini_db_queue),
		len(mini_queue),
	)
}
func waitForAllQueues() {
	for areQueuesEmpty() {
		format := "waiting for queues: BLOCKS: %d MINIS: %d TXS: %d BATCHES: %d SCIDS: %d SCID_DB: %d MINI_DB %d "

		a := []any{
			len(block_processing),
			len(mini_queue),
			len(transaction_processing),
			len(scid_processing),
			len(scid_db_queue),
			len(mini_db_queue),
		}

		format += "\n"

		fmt.Printf(format, a...)
		time.Sleep(time.Millisecond * 200)
	}
}

func find_lowest_height(backup *db.BboltStore, now int64) bool {
	lowest := now
	height, err := backup.GetLastIndexHeight()
	if err != nil {
		fmt.Println(err)
		return false
	}
	lowest = min(lowest, height)
	return (achieved_current_height - day_of_blocks) > lowest
}

func safeString(s string) string {
	if strings.Contains(s, "\n") {
		s = strings.Split(s, "\n")[0]
		if len(s) > 64 {
			s = s[:64]
		}
	} else if len(s) > 64 {
		s = s[:64]
	} else if s == "" {
		s = "null"
	}
	return s
}
