/**
 * Copyright 2019 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package voynicrypto

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io/ioutil"

	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	"github.com/pkg/errors"
	"github.com/xmidt-org/webpa-common/logging"
)

var (
	errIncorrectKeys = errors.New("incorrect keys provided")
)

var (
	hashFunctions = map[string]crypto.Hash{
		"BLAKE2B512": crypto.BLAKE2b_512,
		"SHA1":       crypto.SHA1,
		"SHA512":     crypto.SHA512,
		"MD5":        crypto.MD5,
	}
)

// Config used load the Encrypt or Decrypt
type Config struct {
	// Logger is the go-kit Logger to use for server startup and error logging.  If not
	// supplied, logging.DefaultLogger() is used instead.
	Logger log.Logger `json:"-"`

	// Type is the algorithm type. Like none, box, rsa etc.
	Type AlgorithmType `json:"type"`

	// KID is the key id of the cipher
	KID string `json:"kid,omitempty"`

	// Params to be provided to the algorithm type.
	// For example providing a hash algorithm to rsa.
	Params map[string]string `json:"params,omitempty"`

	// Keys is a map of keys to path. aka senderPrivateKey : private.pem
	Keys map[KeyType]string `json:"keys,omitempty"`
}

// KeyLoader gets the bytes for a key.
type KeyLoader interface {
	GetBytes() ([]byte, error)
}

// EncryptLoader loads an encrypter.
type EncryptLoader interface {
	LoadEncrypt() (Encrypt, error)
}

//DecryptLoader loads a decrypter.
type DecryptLoader interface {
	LoadDecrypt() (Decrypt, error)
}

// FileLoader loads a key from a file.
type FileLoader struct {
	Path string
}

// GetBytes returns the bytes found at the filepath.
func (f *FileLoader) GetBytes() ([]byte, error) {
	return ioutil.ReadFile(f.Path)
}

func CreateFileLoader(keys map[KeyType]string, keyType KeyType) KeyLoader {
	return &FileLoader{
		Path: keys[keyType],
	}
}

// BytesLoader implements the KeyLoader.
type BytesLoader struct {
	Data []byte
}

// GetBytes returns the bytes stored by the BytesLoader
func (b *BytesLoader) GetBytes() ([]byte, error) {
	return b.Data, nil
}

// GetPrivateKey uses a keyloader to load a private key.
func GetPrivateKey(loader KeyLoader) (*rsa.PrivateKey, error) {
	if loader == nil {
		return nil, errors.New("no loader")
	}

	data, err := loader.GetBytes()
	if err != nil {
		return nil, err
	}
	privPem, _ := pem.Decode(data)
	if privPem.Type != "RSA PRIVATE KEY" {
		return nil, errors.New("incorrect pem type: " + privPem.Type)
	}

	var parsedKey interface{}
	if parsedKey, err = x509.ParsePKCS1PrivateKey(privPem.Bytes); err != nil {
		return nil, err
	}

	if privateKey, ok := parsedKey.(*rsa.PrivateKey); !ok {
		return nil, errors.New("failed convert parsed key to private key")
	} else {
		return privateKey, nil
	}
}

// GetPublicKey uses a keyloader to load a public key.
func GetPublicKey(loader KeyLoader) (*rsa.PublicKey, error) {
	if loader == nil {
		return nil, errors.New("no loader")
	}

	data, err := loader.GetBytes()
	if err != nil {
		return nil, err
	}
	publicPem, _ := pem.Decode(data)
	if publicPem.Type != "RSA PUBLIC KEY" {
		return nil, errors.New("incorrect pem type: " + publicPem.Type)
	}

	var parsedKey interface{}
	if parsedKey, err = x509.ParsePKCS1PublicKey(publicPem.Bytes); err != nil {
		return nil, emperror.Wrap(err, "failed to load public key x509.ParsePKCS1PublicKey")
	}

	if publicKey, ok := parsedKey.(*rsa.PublicKey); !ok {
		return nil, errors.New("failed convert parsed key to public key")
	} else {
		return publicKey, nil
	}
}

// LoadEncrypt uses the config to load an encrypter.
func (config *Config) LoadEncrypt() (Encrypt, error) {
	var err error
	if config.Logger == nil {
		config.Logger = logging.DefaultLogger()
	}
	logging.Debug(config.Logger).Log(logging.MessageKey(), "new encrypter", "config", config)

	switch config.Type {
	case None:
		return DefaultCipherEncrypter(), nil
	case Box:
		if !hasBothEncryptKeys(config.Keys) {
			err = errIncorrectKeys
			break
		}
		boxLoader := BoxLoader{
			KID:        config.KID,
			PrivateKey: CreateFileLoader(config.Keys, SenderPrivateKey),
			PublicKey:  CreateFileLoader(config.Keys, RecipientPublicKey),
		}
		return boxLoader.LoadEncrypt()
	case RSASymmetric:
		if _, ok := config.Keys[PublicKey]; !ok {
			err = errIncorrectKeys
			break
		}
		rsaLoader := RSALoader{
			KID:       config.KID,
			Hash:      &BasicHashLoader{HashName: config.Params["hash"]},
			PublicKey: CreateFileLoader(config.Keys, PublicKey),
		}
		return rsaLoader.LoadEncrypt()
	case RSAAsymmetric:
		if !hasBothEncryptKeys(config.Keys) {
			err = errIncorrectKeys
			break
		}
		rsaLoader := RSALoader{
			KID:        config.KID,
			Hash:       &BasicHashLoader{HashName: config.Params["hash"]},
			PrivateKey: CreateFileLoader(config.Keys, SenderPrivateKey),
			PublicKey:  CreateFileLoader(config.Keys, RecipientPublicKey),
		}
		return rsaLoader.LoadEncrypt()
	default:
		err = errors.New("no algorithm type specified")
	}

	return DefaultCipherEncrypter(), emperror.Wrap(err, "failed to load custom algorithm")
}

// LoadDecrypt uses the config to load a decrypter.
func (config *Config) LoadDecrypt() (Decrypt, error) {
	var err error
	if config.Logger == nil {
		config.Logger = logging.DefaultLogger()
	}
	logging.Debug(config.Logger).Log(logging.MessageKey(), "new decrypter", "config", config)

	switch config.Type {
	case None:
		return DefaultCipherDecrypter(), nil
	case Box:
		if !hasBothDecryptKeys(config.Keys) {
			err = errIncorrectKeys
			break
		}
		boxLoader := BoxLoader{
			KID:        config.KID,
			PrivateKey: CreateFileLoader(config.Keys, RecipientPrivateKey),
			PublicKey:  CreateFileLoader(config.Keys, SenderPublicKey),
		}
		return boxLoader.LoadDecrypt()
	case RSASymmetric:
		if _, ok := config.Keys[PrivateKey]; !ok {
			err = errIncorrectKeys
			break
		}
		rsaLoader := RSALoader{
			KID:        config.KID,
			Hash:       &BasicHashLoader{HashName: config.Params["hash"]},
			PrivateKey: CreateFileLoader(config.Keys, PrivateKey),
		}
		return rsaLoader.LoadDecrypt()
	case RSAAsymmetric:
		if !hasBothDecryptKeys(config.Keys) {
			err = errIncorrectKeys
			break
		}
		rsaLoader := RSALoader{
			KID:        config.KID,
			Hash:       &BasicHashLoader{HashName: config.Params["hash"]},
			PrivateKey: CreateFileLoader(config.Keys, RecipientPrivateKey),
			PublicKey:  CreateFileLoader(config.Keys, SenderPublicKey),
		}
		return rsaLoader.LoadDecrypt()
	default:
		err = errors.New("no algorithm type specified")
	}

	return DefaultCipherDecrypter(), emperror.Wrap(err, "failed to load custom algorithm")
}
