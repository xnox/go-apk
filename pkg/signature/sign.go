// Copyright 2023 Chainguard, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package signature

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto"
	_ "crypto/sha1" //nolint:gosec
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/gzip"

	"github.com/psanford/memfs"

	"github.com/chainguard-dev/go-apk/pkg/tarball"
)

func SignIndex(ctx context.Context, signingKey string, indexFile string) error {
	is, err := indexIsAlreadySigned(indexFile)
	if err != nil {
		return err
	}
	if is {
		log.Printf("index %s is already signed, doing nothing", indexFile)
		return nil
	}

	log.Printf("signing index %s with key %s", indexFile, signingKey)

	sigs := []struct {
		digestType crypto.Hash
		filename   string
	}{
		{
			crypto.SHA1,
			"RSA",
		},
	}

	indexData, err := os.ReadFile(indexFile)
	if err != nil {
		fmt.Errorf("unable to read index for signing: %w", err)
	}

	sigFS := memfs.New()

	for _, sig := range sigs {
		indexDigest := sig.digestType.New().Sum(indexData)

		sigData, err := RSASignDigest(indexDigest, sig.digestType, signingKey, "")
		if err != nil {
			return fmt.Errorf("unable to sign index: %w", err)
		}

		log.Printf("appending signature %s to index %s", sig.filename, indexFile)

		if err := sigFS.WriteFile(fmt.Sprintf(".SIGN.%s.%s.pub", sig.filename, filepath.Base(signingKey)), sigData, 0644); err != nil {
			return fmt.Errorf("unable to append signature: %w", err)
		}
	}

	// prepare control.tar.gz
	multitarctx, err := tarball.NewContext(
		tarball.WithOverrideUIDGID(0, 0),
		tarball.WithOverrideUname("root"),
		tarball.WithOverrideGname("root"),
		tarball.WithSkipClose(true),
	)
	if err != nil {
		return fmt.Errorf("unable to build tarball context: %w", err)
	}

	log.Printf("writing signed index to %s", indexFile)

	var sigBuffer bytes.Buffer
	if err := multitarctx.WriteTargz(ctx, &sigBuffer, sigFS, sigFS); err != nil {
		return fmt.Errorf("unable to write signature tarball: %w", err)
	}

	idx, err := os.Create(indexFile)
	if err != nil {
		return fmt.Errorf("unable to open index for writing: %w", err)
	}
	defer idx.Close()

	if _, err := io.Copy(idx, &sigBuffer); err != nil {
		return fmt.Errorf("unable to write index signature: %w", err)
	}

	if _, err := idx.Write(indexData); err != nil {
		return fmt.Errorf("unable to write index data: %w", err)
	}

	log.Printf("signed index %s with key %s", indexFile, signingKey)

	return nil
}

func indexIsAlreadySigned(indexFile string) (bool, error) {
	index, err := os.Open(indexFile)
	if err != nil {
		return false, fmt.Errorf("cannot open index file %s: %w", indexFile, err)
	}
	defer index.Close()

	gzi, err := gzip.NewReader(index)
	if err != nil {
		return false, fmt.Errorf("cannot open index file %s as gzip: %w", indexFile, err)
	}
	defer gzi.Close()

	tari := tar.NewReader(gzi)
	for {
		hdr, err := tari.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false, fmt.Errorf("cannot read tar index %s: %w", indexFile, err)
		}

		if strings.HasPrefix(hdr.Name, ".SIGN.RSA") {
			return true, nil
		}
	}

	return false, nil
}

func ReadAndHashIndexFile(indexFile string, digestType crypto.Hash) ([]byte, []byte, error) {
	indexBuf, err := os.ReadFile(indexFile)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to read index for signing: %w", err)
	}

	indexDigest := digestType.New().Sum(indexBuf)

	return indexBuf, indexDigest, err
}
