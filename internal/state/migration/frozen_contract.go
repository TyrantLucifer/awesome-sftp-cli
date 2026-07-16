package migration

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	version1ContractDigest     = "659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64"
	version1ContractGZIPBase64 = "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"
)

var frozenVersion1Contract = mustDecodeVersion1Contract()

func Version1SchemaContract() []byte {
	result := make([]byte, len(frozenVersion1Contract))
	copy(result, frozenVersion1Contract)
	return result
}

func ValidateVersion1SchemaContract(ctx context.Context, connection *sql.Conn) error {
	actual, err := BuildSchemaContract(ctx, connection, 1)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, frozenVersion1Contract) {
		digest := sha256.Sum256(actual)
		return fmt.Errorf("validate version 1 schema contract: digest %x, want %s", digest, version1ContractDigest)
	}
	return nil
}

func mustDecodeVersion1Contract() []byte {
	compressed, err := base64.StdEncoding.DecodeString(version1ContractGZIPBase64)
	if err != nil {
		panic(fmt.Sprintf("decode frozen version 1 contract: %v", err))
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		panic(fmt.Sprintf("open frozen version 1 contract: %v", err))
	}
	contract, err := io.ReadAll(io.LimitReader(reader, 1024*1024))
	if err != nil {
		panic(fmt.Sprintf("read frozen version 1 contract: %v", err))
	}
	if err := reader.Close(); err != nil {
		panic(fmt.Sprintf("close frozen version 1 contract: %v", err))
	}
	digest := sha256.Sum256(contract)
	if hex.EncodeToString(digest[:]) != version1ContractDigest {
		panic(fmt.Sprintf("frozen version 1 contract digest = %x, want %s", digest, version1ContractDigest))
	}
	return contract
}
