package externalprocess

import (
	"fmt"
	"os"
)

type frozenFileIdentity struct {
	info       os.FileInfo
	changeTime int64
}

func freezeFileIdentity(info os.FileInfo) (frozenFileIdentity, error) {
	if info == nil {
		return frozenFileIdentity{}, fmt.Errorf("freeze file identity: nil metadata")
	}
	changeTime, err := platformChangeTime(info)
	if err != nil {
		return frozenFileIdentity{}, err
	}
	return frozenFileIdentity{info: info, changeTime: changeTime}, nil
}

func (identity frozenFileIdentity) matches(info os.FileInfo) bool {
	if identity.info == nil || info == nil || !os.SameFile(identity.info, info) || identity.info.Size() != info.Size() || identity.info.Mode() != info.Mode() || !identity.info.ModTime().Equal(info.ModTime()) {
		return false
	}
	changeTime, err := platformChangeTime(info)
	return err == nil && changeTime == identity.changeTime
}
