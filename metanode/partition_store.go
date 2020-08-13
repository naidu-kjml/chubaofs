// Copyright 2018 The Chubao Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package metanode

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/chubaofs/chubaofs/util/log"

	"github.com/chubaofs/chubaofs/proto"
	"github.com/chubaofs/chubaofs/util/errors"
	mmap "github.com/edsrzf/mmap-go"
)

const (
	snapshotDir     = "snapshot"
	snapshotDirTmp  = ".snapshot"
	snapshotBackup  = ".snapshot_backup"
	inodeFile       = "inode"
	dentryFile      = "dentry"
	extendFile      = "extend"
	multipartFile   = "multipart"
	applyIDFile     = "apply"
	SnapshotSign    = ".sign"
	metadataFile    = "meta"
	metadataFileTmp = ".meta"
)

func (mp *MetaPartition) loadMetadata() (err error) {
	metaFile := path.Join(mp.config.RootDir, metadataFile)
	fp, err := os.OpenFile(metaFile, os.O_RDONLY, 0644)
	if err != nil {
		err = errors.NewErrorf("[loadMetadata]: OpenFile %s", err.Error())
		return
	}
	defer fp.Close()
	data, err := ioutil.ReadAll(fp)
	if err != nil || len(data) == 0 {
		err = errors.NewErrorf("[loadMetadata]: ReadFile %s, data: %s", err.Error(),
			string(data))
		return
	}
	mConf := &MetaPartitionConfig{}
	if err = json.Unmarshal(data, mConf); err != nil {
		err = errors.NewErrorf("[loadMetadata]: Unmarshal MetaPartitionConfig %s",
			err.Error())
		return
	}

	if mConf.checkMeta() != nil {
		return
	}
	mp.config.PartitionId = mConf.PartitionId
	mp.config.VolName = mConf.VolName
	mp.config.Start = mConf.Start
	mp.config.End = mConf.End
	mp.config.Peers = mConf.Peers
	//mp.config.StoreType = mConf.StoreType :TODO use config
	mp.SetCursor(mp.config.Start)

	log.LogInfof("loadMetadata: load complete: partitionID(%v) volume(%v) range(%v,%v) cursor(%v)",
		mp.config.PartitionId, mp.config.VolName, mp.config.Start, mp.config.End, mp.GetCursor())
	return
}

func (mp *MetaPartition) loadInode(rootDir string) (err error) {
	var numInodes uint64
	defer func() {
		if err == nil {
			log.LogInfof("loadInode: load complete: partitonID(%v) volume(%v) numInodes(%v)",
				mp.config.PartitionId, mp.config.VolName, numInodes)
		}
	}()
	filename := path.Join(rootDir, inodeFile)
	if _, err = os.Stat(filename); err != nil {
		err = nil
		return
	}
	fp, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		err = errors.NewErrorf("[loadInode] OpenFile: %s", err.Error())
		return
	}
	defer fp.Close()
	reader := bufio.NewReaderSize(fp, 4*1024*1024)
	inoBuf := make([]byte, 4)
	for {
		inoBuf = inoBuf[:4]
		// first read length
		_, err = io.ReadFull(reader, inoBuf)
		if err != nil {
			if err == io.EOF {
				err = nil
				return
			}
			err = errors.NewErrorf("[loadInode] ReadHeader: %s", err.Error())
			return
		}
		length := binary.BigEndian.Uint32(inoBuf)

		// next read body
		if uint32(cap(inoBuf)) >= length {
			inoBuf = inoBuf[:length]
		} else {
			inoBuf = make([]byte, length)
		}
		_, err = io.ReadFull(reader, inoBuf)
		if err != nil {
			err = errors.NewErrorf("[loadInode] ReadBody: %s", err.Error())
			return
		}
		ino := NewInode(0, 0)
		if err = ino.Unmarshal(inoBuf); err != nil {
			err = errors.NewErrorf("[loadInode] Unmarshal: %s", err.Error())
			return
		}
		mp.fsmCreateInode(ino)
		mp.checkAndInsertFreeList(ino)
		if mp.GetCursor() < ino.Inode {
			mp.SetCursor(ino.Inode)
		}
		numInodes += 1
	}
}

// Load dentry from the dentry snapshot.
func (mp *MetaPartition) loadDentry(rootDir string) (err error) {
	var numDentries uint64
	defer func() {
		if err == nil {
			log.LogInfof("loadDentry: load complete: partitonID(%v) volume(%v) numDentries(%v)",
				mp.config.PartitionId, mp.config.VolName, numDentries)
		}
	}()
	filename := path.Join(rootDir, dentryFile)
	if _, err = os.Stat(filename); err != nil {
		err = nil
		return
	}
	fp, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		if err == os.ErrNotExist {
			err = nil
			return
		}
		err = errors.NewErrorf("[loadDentry] OpenFile: %s", err.Error())
		return
	}

	defer fp.Close()
	reader := bufio.NewReaderSize(fp, 4*1024*1024)
	dentryBuf := make([]byte, 4)
	for {
		dentryBuf = dentryBuf[:4]
		// First Read 4byte header length
		_, err = io.ReadFull(reader, dentryBuf)
		if err != nil {
			if err == io.EOF {
				err = nil
				return
			}
			err = errors.NewErrorf("[loadDentry] ReadHeader: %s", err.Error())
			return
		}

		length := binary.BigEndian.Uint32(dentryBuf)

		// next read body
		if uint32(cap(dentryBuf)) >= length {
			dentryBuf = dentryBuf[:length]
		} else {
			dentryBuf = make([]byte, length)
		}
		_, err = io.ReadFull(reader, dentryBuf)
		if err != nil {
			err = errors.NewErrorf("[loadDentry]: ReadBody: %s", err.Error())
			return
		}
		dentry := &Dentry{}
		if err = dentry.Unmarshal(dentryBuf); err != nil {
			err = errors.NewErrorf("[loadDentry] Unmarshal: %s", err.Error())
			return
		}
		if status := mp.fsmCreateDentry(dentry, true); status != proto.OpOk {
			err = errors.NewErrorf("[loadDentry] createDentry dentry: %v, resp code: %d", dentry, status)
			return
		}
		numDentries += 1
	}
}

func (mp *MetaPartition) loadExtend(rootDir string) error {
	var err error
	filename := path.Join(rootDir, extendFile)
	if _, err = os.Stat(filename); err != nil {
		return nil
	}
	fp, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		_ = fp.Close()
	}()
	var mem mmap.MMap
	if mem, err = mmap.Map(fp, mmap.RDONLY, 0); err != nil {
		return err
	}
	defer func() {
		_ = mem.Unmap()
	}()
	var offset, n int
	// read number of extends
	var numExtends uint64
	numExtends, n = binary.Uvarint(mem)
	offset += n
	for i := uint64(0); i < numExtends; i++ {
		// read length
		var numBytes uint64
		numBytes, n = binary.Uvarint(mem[offset:])
		offset += n
		var extend *Extend
		if extend, err = NewExtendFromBytes(mem[offset : offset+int(numBytes)]); err != nil {
			return err
		}
		log.LogDebugf("loadExtend: new extend from bytes: partitionID（%v) volume(%v) inode(%v)",
			mp.config.PartitionId, mp.config.VolName, extend.inode)
		_ = mp.fsmSetXAttr(extend)
		offset += int(numBytes)
	}
	log.LogInfof("loadExtend: load complete: partitionID(%v) volume(%v) numExtends(%v) filename(%v)",
		mp.config.PartitionId, mp.config.VolName, numExtends, filename)
	return nil
}

func (mp *MetaPartition) loadMultipart(rootDir string) error {
	var err error
	filename := path.Join(rootDir, multipartFile)
	if _, err = os.Stat(filename); err != nil {
		return nil
	}
	fp, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		_ = fp.Close()
	}()
	var mem mmap.MMap
	if mem, err = mmap.Map(fp, mmap.RDONLY, 0); err != nil {
		return err
	}
	defer func() {
		_ = mem.Unmap()
	}()
	var offset, n int
	// read number of extends
	var numMultiparts uint64
	numMultiparts, n = binary.Uvarint(mem)
	offset += n
	for i := uint64(0); i < numMultiparts; i++ {
		// read length
		var numBytes uint64
		numBytes, n = binary.Uvarint(mem[offset:])
		offset += n
		var multipart *Multipart
		multipart = MultipartFromBytes(mem[offset : offset+int(numBytes)])
		log.LogDebugf("loadMultipart: create multipart from bytes: partitionID（%v) multipartID(%v)", mp.config.PartitionId, multipart.id)
		mp.fsmCreateMultipart(multipart)
		offset += int(numBytes)
	}
	log.LogInfof("loadMultipart: load complete: partitionID(%v) numMultiparts(%v) filename(%v)",
		mp.config.PartitionId, numMultiparts, filename)
	return nil
}

func (mp *MetaPartition) loadApplyID(rootDir string) (err error) {
	filename := path.Join(rootDir, applyIDFile)
	if _, err = os.Stat(filename); err != nil {
		err = nil
		return
	}
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		if err == os.ErrNotExist {
			err = nil
			return
		}
		err = errors.NewErrorf("[loadApplyID] OpenFile: %s", err.Error())
		return
	}
	if len(data) == 0 {
		err = errors.NewErrorf("[loadApplyID]: ApplyID is empty")
		return
	}
	var cursor uint64
	if strings.Contains(string(data), "|") {
		_, err = fmt.Sscanf(string(data), "%d|%d", &mp.applyID, &cursor)
	} else {
		_, err = fmt.Sscanf(string(data), "%d", &mp.applyID)
	}
	if err != nil {
		err = errors.NewErrorf("[loadApplyID] ReadApplyID: %s", err.Error())
		return
	}

	if cursor > mp.GetCursor() {
		mp.SetCursor(cursor)
	}
	mp.updatePersistedApplyID(cursor)
	log.LogInfof("loadApplyID: load complete: partitionID(%v) volume(%v) applyID(%v) filename(%v)",
		mp.config.PartitionId, mp.config.VolName, mp.applyID, filename)
	return
}

func (mp *MetaPartition) persistMetadata() (err error) {
	if err = mp.config.checkMeta(); err != nil {
		err = errors.NewErrorf("[persistMetadata]->%s", err.Error())
		return
	}

	// TODO Unhandled errors
	os.MkdirAll(mp.config.RootDir, 0755)
	filename := path.Join(mp.config.RootDir, metadataFileTmp)
	fp, err := os.OpenFile(filename, os.O_RDWR|os.O_TRUNC|os.O_APPEND|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	defer func() {
		// TODO Unhandled errors
		fp.Sync()
		fp.Close()
		os.Remove(filename)
	}()

	data, err := json.Marshal(mp.config)
	if err != nil {
		return
	}
	if _, err = fp.Write(data); err != nil {
		return
	}
	if err = os.Rename(filename, path.Join(mp.config.RootDir, metadataFile)); err != nil {
		return
	}
	log.LogInfof("persistMetata: persist complete: partitionID(%v) volume(%v) range(%v,%v) cursor(%v)",
		mp.config.PartitionId, mp.config.VolName, mp.config.Start, mp.config.End, mp.GetCursor())
	return
}

func (mp *MetaPartition) storeApplyID(rootDir string, sm *storeMsg) (err error) {
	filename := path.Join(rootDir, applyIDFile)
	fp, err := os.OpenFile(filename, os.O_RDWR|os.O_APPEND|os.O_TRUNC|os.
		O_CREATE, 0755)
	if err != nil {
		return
	}
	defer func() {
		err = fp.Sync()
		fp.Close()
	}()
	if _, err = fp.WriteString(fmt.Sprintf("%d|%d", sm.applyIndex, mp.GetCursor())); err != nil {
		return
	}
	log.LogInfof("storeApplyID: store complete: partitionID(%v) volume(%v) applyID(%v)",
		mp.config.PartitionId, mp.config.VolName, sm.applyIndex)
	return
}

func (mp *MetaPartition) storeInode(rootDir string, sm *storeMsg) (crc uint32, err error) {
	filename := path.Join(rootDir, inodeFile)
	fp, err := os.OpenFile(filename, os.O_RDWR|os.O_TRUNC|os.O_APPEND|os.
		O_CREATE, 0755)
	if err != nil {
		return
	}
	writer := bufio.NewWriter(fp)
	defer func() {
		if err = writer.Flush(); err != nil {
			return
		}
		err = fp.Sync()
		// TODO Unhandled errors
		fp.Close()
	}()
	sign := crc32.NewIEEE()
	var (
		buff  = bytes.NewBuffer(nil)
		reuse = bytes.NewBuffer(nil)
	)

	err = sm.snapshot.Range(InodeType, func(v []byte) (b bool, err error) {
		ino := &Inode{}
		if err := ino.Unmarshal(v); err != nil {
			return false, err
		}

		buff.Reset()
		if err = ino.WriteTo(buff, reuse); err != nil {
			return false, err
		}
		var data = buff.Bytes()
		// write length
		if err = binary.Write(writer, binary.BigEndian, uint32(len(data))); err != nil {
			return false, err
		}
		if err = binary.Write(sign, binary.BigEndian, uint32(len(data))); err != nil {
			return false, err
		}
		if _, err = writer.Write(data); err != nil {
			return false, err

		}
		if _, err = sign.Write(data); err != nil {
			return false, err
		}
		return true, nil
	})

	if err != nil {
		log.LogErrorf("range inode has err:[%s]", err.Error())
		return
	}

	crc = sign.Sum32()
	log.LogInfof("storeInode: store complete: partitoinID(%v) volume(%v) crc(%v)",
		mp.config.PartitionId, mp.config.VolName, crc)
	return
}

func (mp *MetaPartition) storeDentry(rootDir string, sm *storeMsg) (crc uint32, err error) {
	filename := path.Join(rootDir, dentryFile)
	fp, err := os.OpenFile(filename, os.O_RDWR|os.O_TRUNC|os.O_APPEND|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	var writer = bufio.NewWriter(fp)
	defer func() {
		if err = writer.Flush(); err != nil {
			return
		}
		err = fp.Sync()
		// TODO Unhandled errors
		fp.Close()
	}()
	sign := crc32.NewIEEE()

	err = sm.snapshot.Range(DentryType, func(data []byte) (b bool, err error) {
		// write length
		if err = binary.Write(writer, binary.BigEndian, uint32(len(data))); err != nil {
			return false, err
		}
		if err = binary.Write(sign, binary.BigEndian, uint32(len(data))); err != nil {
			return false, err
		}
		if _, err = writer.Write(data); err != nil {
			return false, err
		}
		if _, err = sign.Write(data); err != nil {
			return false, err
		}
		return true, nil
	})

	if err != nil {
		log.LogErrorf("range dentry has err:[%s]", err.Error())
		return
	}

	crc = sign.Sum32()
	log.LogInfof("storeDentry: store complete: partitoinID(%v) volume(%v) crc(%v)",
		mp.config.PartitionId, mp.config.VolName, crc)
	return
}

func (mp *MetaPartition) storeExtend(rootDir string, sm *storeMsg) (crc uint32, err error) {
	var fp = path.Join(rootDir, extendFile)
	var f *os.File
	f, err = os.OpenFile(fp, os.O_RDWR|os.O_TRUNC|os.O_APPEND|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	defer func() {
		closeErr := f.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	var writer = bufio.NewWriterSize(f, 4*1024*1024)
	var sign = crc32.NewIEEE()
	var varintTmp = make([]byte, binary.MaxVarintLen64)
	var n int
	// write number of extends
	count, err := sm.snapshot.Count(ExtendType)
	if err != nil {
		return 0, err
	}
	n = binary.PutUvarint(varintTmp, count)
	if _, err = writer.Write(varintTmp[:n]); err != nil {
		return
	}
	if _, err = sign.Write(varintTmp[:n]); err != nil {
		return
	}

	err = sm.snapshot.Range(ExtendType, func(data []byte) (b bool, err error) {
		n = binary.PutUvarint(varintTmp, uint64(len(data)))
		if _, err = writer.Write(varintTmp[:n]); err != nil {
			return false, err
		}
		if _, err = sign.Write(varintTmp[:n]); err != nil {
			return false, err
		}
		if _, err = writer.Write(data); err != nil {
			return false, err
		}
		if _, err = sign.Write(data); err != nil {
			return false, err
		}
		return true, nil
	})

	if err != nil {
		return
	}

	if err = writer.Flush(); err != nil {
		return
	}
	if err = f.Sync(); err != nil {
		return
	}
	crc = sign.Sum32()
	log.LogInfof("storeExtend: store complete: partitoinID(%v) volume(%v) crc(%v)", mp.config.PartitionId, mp.config.VolName, crc)
	return
}

func (mp *MetaPartition) storeMultipart(rootDir string, sm *storeMsg) (crc uint32, err error) {
	var fp = path.Join(rootDir, multipartFile)
	var f *os.File
	f, err = os.OpenFile(fp, os.O_RDWR|os.O_TRUNC|os.O_APPEND|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	defer func() {
		closeErr := f.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	var writer = bufio.NewWriterSize(f, 4*1024*1024)
	var crc32 = crc32.NewIEEE()
	var varintTmp = make([]byte, binary.MaxVarintLen64)
	var n int
	// write number of extends
	count, err := sm.snapshot.Count(ExtendType)
	if err != nil {
		return 0, err
	}
	n = binary.PutUvarint(varintTmp, count)
	if _, err = writer.Write(varintTmp[:n]); err != nil {
		return
	}
	if _, err = crc32.Write(varintTmp[:n]); err != nil {
		return
	}

	err = sm.snapshot.Range(MultipartType, func(raw []byte) (b bool, err error) {
		n = binary.PutUvarint(varintTmp, uint64(len(raw)))
		if _, err = writer.Write(varintTmp[:n]); err != nil {
			return false, err
		}
		if _, err = crc32.Write(varintTmp[:n]); err != nil {
			return false, err
		}
		// write raw
		if _, err = writer.Write(raw); err != nil {
			return false, err
		}
		if _, err = crc32.Write(raw); err != nil {
			return false, err
		}
		return true, nil
	})

	if err != nil {
		return
	}

	if err = writer.Flush(); err != nil {
		return
	}
	if err = f.Sync(); err != nil {
		return
	}
	crc = crc32.Sum32()
	log.LogInfof("storeMultipart: store complete: partitoinID(%v) volume(%v)  crc(%v)", mp.config.PartitionId, mp.config.VolName, crc)
	return
}
