package gnomon

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"

	prefixed "github.com/x-cray/logrus-prefixed-formatter"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"

	"github.com/sirupsen/logrus"
)

type SCIDToIndexStage struct {
	Scid   string
	Fsi    *FastSyncImport
	ScVars []*SCIDVariable
	ScCode string
}

type Indexer struct {
	LastIndexedHeight int64
	ChainHeight       int64
	SearchFilter      []string
	SFSCIDExclusion   []string
	BBSBackend        *BboltStore
	Closing           bool
	ValidatedSCs      []string
	Status            string
	sync.RWMutex
}

var Connected bool = false

// local logger
var l *logrus.Entry

func InitLog(args map[string]interface{}, console io.Writer) {
	loglevel_console := logrus.InfoLevel

	if args["--debug"] != nil && args["--debug"].(bool) {
		loglevel_console = logrus.DebugLevel
	}

	Logger = logrus.Logger{
		Out:   console,
		Level: loglevel_console,
		Formatter: &prefixed.TextFormatter{
			ForceColors:     true,
			DisableColors:   false,
			TimestampFormat: "01/02/2006 15:04:05",
			FullTimestamp:   true,
			ForceFormatting: true,
		},
	}
}

func NewIndexer(
	Bbs_backend *BboltStore,
	last_indexedheight int64,
	sfscidexclusion []string,
) *Indexer {

	l = l.WithFields(logrus.Fields{})

	return &Indexer{
		LastIndexedHeight: last_indexedheight,
		SFSCIDExclusion:   sfscidexclusion,
		BBSBackend:        Bbs_backend,
	}
}

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
func (indexer *Indexer) AddSCIDToIndex(scidstoadd SCIDToIndexStage) (err error) {

	if scidstoadd.Scid == "" {
		return errors.New("no scid")
	}

	if scidstoadd.Fsi == nil {
		return errors.New("nothing to import")
	}

	writeWait, _ := time.ParseDuration("10ms")

	for indexer.BBSBackend.Writing {
		if indexer.Closing {
			return
		}
		//l.Debugf("[AddSCIDToIndex-StoreAltDBInput] GravitonDB is writing... sleeping for %v...", writeWait)
		time.Sleep(writeWait)
	}

	indexer.BBSBackend.Writing = true

	// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
	if len(scidstoadd.ScVars) != 0 {
		changed, err := indexer.BBSBackend.StoreSCIDVariableDetails(
			scidstoadd.Scid,
			scidstoadd.ScVars,
			int64(scidstoadd.Fsi.Height),
		)
		if err != nil {
			return err
		}
		if !changed {
			return errors.New("did not write to db")
		}

		changed, err = indexer.BBSBackend.StoreOwner(
			scidstoadd.Scid,
			scidstoadd.Fsi.Owner,
		)
		if err != nil {
			return err
		}
		if !changed {
			return errors.New("did not write to db")
		}

		l.Info("[AddSCIDToIndex] New stored disk: ", fmt.Sprint(len(indexer.BBSBackend.GetAllOwnersAndSCIDs())))
	} else {

		changed, err := indexer.BBSBackend.StoreSCIDInteractionHeight(
			scidstoadd.Scid,
			int64(scidstoadd.Fsi.Height),
		)
		if err != nil {
			return err
		}

		if !changed {
			return errors.New("did not write to db")
		}
		l.Info("[AddSCIDToIndex] New stored disk: ", fmt.Sprint(len(indexer.BBSBackend.GetSCIDInteractionHeight(scidstoadd.Scid))))

	}
	indexer.BBSBackend.Writing = false
	return nil
}

// Gets SC variable details
func GetSCVariables(keysstring map[string]any, keysuint64 map[uint64]any) (variables []*SCIDVariable, err error) {
	//balances = make(map[string]uint64)

	isAlpha := regexp.MustCompile(`^[A-Za-z]+$`).MatchString

	for k, v := range keysstring {
		currVar := &SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case float64:
			currVar.Value = uint64(cval)
		case uint64:
			currVar.Value = cval
		case string:
			// hex decode since all strings are hex encoded
			dstr, _ := hex.DecodeString(cval)
			// Check if dstr is an address raw
			p := new(crypto.Point)
			if err := p.DecodeCompressed(dstr); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(dstr)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							currVar.Value = str
						} else {
							currVar.Value = h.String()
						}
					} else {
						currVar.Value = str
					}
				} else {
					currVar.Value = str
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
						currVar.Value = str
					} else {
						currVar.Value = h.String()
					}
				} else {
					currVar.Value = str
				}
			} else {
				currVar.Value = str
			}
		}
		variables = append(variables, currVar)
	}

	for k, v := range keysuint64 {
		currVar := &SCIDVariable{}
		currVar.Key = k
		switch cval := v.(type) {
		case string:
			// hex decode since all strings are hex encoded
			decd, _ := hex.DecodeString(cval)
			p := new(crypto.Point)
			if err := p.DecodeCompressed(decd); err == nil {

				addr := rpc.NewAddressFromKeys(p)
				currVar.Value = addr.String()
			} else {
				// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
				str := string(decd)
				if len(str) == crypto.HashLength {
					var h crypto.Hash
					copy(h[:crypto.HashLength], []byte(str)[:])

					if len(h.String()) == 64 && !isAlpha(str) {
						if !crypto.HashHexToHash(str).IsZero() {
							currVar.Value = str
						} else {
							currVar.Value = h.String()
						}
					} else {
						currVar.Value = str
					}
				} else {
					currVar.Value = str
				}
			}
		case uint64:
			currVar.Value = cval
		case float64:
			currVar.Value = uint64(cval)
		default:
			// non-string/uint64 (shouldn't be here actually since it's either uint64 or string conversion)
			str := fmt.Sprintf("%v", cval)
			// Check specific patterns which reflect STORE() operations of TXID(), SCID(), etc.
			if len(str) == crypto.HashLength {
				var h crypto.Hash
				copy(h[:crypto.HashLength], []byte(str)[:])

				if len(h.String()) == 64 && !isAlpha(str) {
					if !crypto.HashHexToHash(str).IsZero() {
						currVar.Value = str
					} else {
						currVar.Value = h.String()
					}
				} else {
					currVar.Value = str
				}
			} else {
				currVar.Value = str
			}
		}
		variables = append(variables, currVar)
	}

	return variables, nil
}
