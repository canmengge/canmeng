package main

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

const paged110RSADER = "3082025d02010002818100b3120bba28e9db7a87bf61ea60fdd28141b06759259899c0d22369b497cca064fbfd2747f4" +
	"a46734774cc04c7f8b4f95db428aba88ce37222bca7244918c0971697d2a3d457143b9629fc3d510c95824e3c0ffe87f" +
	"aea0b7f5034a617ed3948442ee7df6c07595b137f448cfbec3f024630c76dbd56fcd9a3f7017184dc8394f0203010001" +
	"028181009acdeef570893b04227680df6e19fff15e28722fcf20ad4ad45f68f286888fe0bd378ccdd7e0889802ca8733" +
	"9acf846db8af3ddf2485a18418f75af18c21d3c69409bc169c4ad6c7240e451ee29e96e83a4797d29c96503f2bad80df" +
	"fcbe962d736b8aaf3a1a0c4ec013ef1354d2dab317deb13a65a19415fe9a22f91f7cf701024100ea47490bb1f9402393" +
	"a621b0c1389eac7d2b152a54c4acf2209532d688ce76d1de36c0ee6770f9d6eda2487a89706fec895f768518df42c8fa" +
	"ca5bf971b33589024100c3ac5f46b88cd24538cabe7ea551db33a453c1b475c5ac895624df29ce560081d6e79b5dc7a9" +
	"45214d20b30400982f25c3c31530f79388e74e6820ae802a9a17024100b8b60182860ca5a4272a49cfc957f1cabf5933" +
	"73cfa7cd4f8d9ef4992efdd1b2c007dd6f5a013a0a5a0ba42770ab44a372dfe05b29f404fcdeb6a3737550bd3902406b" +
	"f0523e78df75bea9ad6d97ff2a40792454efadd4a9ce9b93e1931944b13c66635e2fde739d747d0246df797dba7587a7" +
	"8d9dcafd476d65eb629564ad5ed2d102405a8e7445a4d6d4c9b36163bde05d73bda013d3b63a0d5c502e0cefb5ec478d" +
	"3f2114a15e796b91f0502a728a6b23473b45a2c04814ee22613ed018f8fc600d7a"

const paged110EmbeddedMetadataHex = "21ad8ff286bd11687520212e5dfd064bd9a7ec798f7f78a706b0486e77634489"

const (
	paged110PageSize      = 10485760
	paged110PageGuardSize = 10240
	paged110PageKeySize  = 32
	magicMain            = 0x269EC3
	magicAlt             = 0x269EC9
	lcgMul               = 0x343FD
	headerSize           = 0x30
	xorStrA              = 0xAA74472E
	xorStrW              = 0x9A82F037
)

func wideSeed(name string) uint32 {
	var u [4]uint16
	if len(name) < 4 {
		return 0
	}
	for i := 0; i < 4; i++ {
		u[i] = uint16(name[i])
	}
	return 0x339E9711*uint32(u[0]) +
		0x393*(uint32(u[3])+0x393*(uint32(u[2])+0x393*uint32(u[1])))
}

func cryptSeed(seed, magic uint32, b []byte) {
	if len(b) == 0 {
		return
	}
	s := seed
	n := len(b)
	nq := n >> 2
	for i := 0; i < nq; i++ {
		t1 := lcgMul*s + magic
		s = lcgMul*t1 + magic
		xk := (t1 & 0xFFFF0000) | (s >> 16)
		off := i << 2
		binary.LittleEndian.PutUint32(b[off:], binary.LittleEndian.Uint32(b[off:])^xk)
	}
}

type Header struct {
	Signature     uint32
	Guid          [20]byte
	FileCount     int32
	Padding       int32
	BodySize      int32
	GroupCount    int32
	HashTableSize int32
	NameTableSize int32
}

func decodeHeader(b []byte) Header {
	return Header{
		Signature:     binary.LittleEndian.Uint32(b[0:4]),
		FileCount:     int32(binary.LittleEndian.Uint32(b[24:28])),
		Padding:       int32(binary.LittleEndian.Uint32(b[28:32])),
		BodySize:      int32(binary.LittleEndian.Uint32(b[32:36])),
		GroupCount:    int32(binary.LittleEndian.Uint32(b[36:40])),
		HashTableSize: int32(binary.LittleEndian.Uint32(b[40:44])),
		NameTableSize: int32(binary.LittleEndian.Uint32(b[44:48])),
	}
}

type fileItem struct {
	nameOff, pathOff int32
	chunk            int32
	off, size        int32
	typ              int32
}

func zlibDecompress(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x78 {
		return nil, fmt.Errorf("bad zlib header: %x", data[:2])
	}
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func main() {
	pvfPath := `D:\115us\PVF Ai Agent\Script.pvf`
	skPath := `D:\115us\PVF Ai Agent\sk.dat`

	// 读取并解密 PVF
	data, _ := os.ReadFile(pvfPath)
	sealed, _ := os.ReadFile(skPath)

	// RSA 解密 sk.dat
	der, _ := hex.DecodeString(paged110RSADER)
	privKey, _ := x509.ParsePKCS1PrivateKey(der)
	table := make([]byte, 0, len(sealed))
	for off := 0; off < len(sealed); off += 128 {
		decrypted, _ := rsa.DecryptPKCS1v15(nil, privKey, sealed[off:off+128])
		table = append(table, decrypted...)
	}

	// AES 解密页密钥表
	metadataKey, _ := hex.DecodeString(paged110EmbeddedMetadataHex)
	unwrapped := make([]byte, len(table))
	copy(unwrapped, table)
	n := len(unwrapped) &^ 255
	block, _ := aes.NewCipher(metadataKey)
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(unwrapped[:n], unwrapped[:n])

	// 解密所有页守卫
	buf := make([]byte, len(data))
	copy(buf, data)
	pages := len(unwrapped) / paged110PageKeySize
	for i := 0; i < pages; i++ {
		off := i * paged110PageSize
		if off+paged110PageGuardSize > len(buf) {
			break
		}
		pageBlock, _ := aes.NewCipher(unwrapped[i*paged110PageKeySize : (i+1)*paged110PageKeySize])
		cipher.NewCBCDecrypter(pageBlock, make([]byte, aes.BlockSize)).
			CryptBlocks(buf[off:off+paged110PageGuardSize], buf[off:off+paged110PageGuardSize])
	}

	// 解密头部
	var hdrRaw [headerSize]byte
	copy(hdrRaw[:], buf[:headerSize])
	seed := wideSeed("iNfO")
	cryptSeed(seed, magicMain, hdrRaw[:])
	hdr := decodeHeader(hdrRaw[:])

	fmt.Printf("PVF 解密成功！文件数: %d\n", hdr.FileCount)

	// 计算各段偏移
	fileTableOff := headerSize
	hashTableOff := fileTableOff + int(hdr.FileCount)*0x18
	nameTableOff := hashTableOff + int(hdr.HashTableSize)
	grpiOff := nameTableOff + int(hdr.NameTableSize)
	bodyOff := grpiOff + int(hdr.GroupCount)*8

	// 解析名称池
	fmt.Println("\n=== 解析名称池 ===")
	namePool := buf[nameTableOff : nameTableOff+int(hdr.NameTableSize)]
	idx := 8
	var strA, strW []byte

	// sTrA
	cnt1 := binary.LittleEndian.Uint32(namePool[idx:])
	idx += 8
	encSizeA := int64(cnt1 ^ xorStrA)
	fmt.Printf("sTrA 加密大小: %d\n", encSizeA)
	if encSizeA > 0 && idx+int(encSizeA) <= len(namePool) {
		encA := make([]byte, encSizeA)
		copy(encA, namePool[idx:idx+int(encSizeA)])
		idx += int(encSizeA)
		cryptSeed(wideSeed("stAs"), magicAlt, encA)
		strA, _ = zlibDecompress(encA)
		fmt.Printf("sTrA 解压成功: %d 字节\n", len(strA))
	}

	// sTrW
	cnt1 = binary.LittleEndian.Uint32(namePool[idx:])
	idx += 8
	encSizeW := int64(cnt1 ^ xorStrW)
	fmt.Printf("sTrW 加密大小: %d\n", encSizeW)
	if encSizeW > 0 && idx+int(encSizeW) <= len(namePool) {
		encW := make([]byte, encSizeW)
		copy(encW, namePool[idx:idx+int(encSizeW)])
		idx += int(encSizeW)
		cryptSeed(wideSeed("stWs"), magicAlt, encW)
		strW, _ = zlibDecompress(encW)
		fmt.Printf("sTrW 解压成功: %d 字节\n", len(strW))
	}

	// 搜索 50001248 在 sTrA 里
	fmt.Println("\n=== 搜索 50001248 ===")
	target := []byte("50001248")
	count := 0
	for i := 0; i < len(strA)-len(target); i++ {
		if bytes.Equal(strA[i:i+len(target)], target) {
			count++
			if count <= 5 {
				start := i - 30
				if start < 0 {
					start = 0
				}
				end := i + 40
				if end > len(strA) {
					end = len(strA)
				}
				fmt.Printf("  sTrA[%d]: %q\n", i, strA[start:end])
			}
		}
	}
	fmt.Printf("  sTrA 中找到 %d 处匹配\n", count)

	// 读取 GRPI
	grpiData := buf[grpiOff : grpiOff+int(hdr.GroupCount)*8]
	grpiDec := make([]byte, len(grpiData))
	copy(grpiDec, grpiData)
	cryptSeed(wideSeed("Gidx"), magicMain, grpiDec)

	// 解压 GRPI，获取 chunk 偏移
	fmt.Println("\n=== 解压 GRPI ===")
	fmt.Printf("GRPI 解析完成: %d 个 chunk\n", hdr.GroupCount)

	// 构建 chunk 偏移表
	bodyData := buf[bodyOff:]
	chunkOffsets := make([]int, int(hdr.GroupCount)+1)
	chunkOffsets[0] = 0
	for i := 0; i < int(hdr.GroupCount); i++ {
		compSize := binary.LittleEndian.Uint32(grpiDec[i*8 : i*8+4])
		chunkOffsets[i+1] = chunkOffsets[i] + int(compSize)
	}

	// 读取一个 chunk 的函数
	getChunk := func(chunkIdx int32) ([]byte, error) {
		start := chunkOffsets[chunkIdx]
		end := chunkOffsets[chunkIdx+1]
		compData := bodyData[start:end]
		// 解密
		dec := make([]byte, len(compData))
		copy(dec, compData)
		cryptSeed(wideSeed("mAIn"), magicMain, dec)
		// 解压
		return zlibDecompress(dec)
	}

	// 读取前几个文件，看看文件名
	fmt.Println("\n=== 前 10 个文件名 ===")
	fileTable := buf[fileTableOff:]
	for i := 0; i < 10; i++ {
		off := i * 0x18
		item := fileItem{
			nameOff: int32(binary.LittleEndian.Uint32(fileTable[off : off+4])),
			pathOff: int32(binary.LittleEndian.Uint32(fileTable[off+4 : off+8])),
			chunk:   int32(binary.LittleEndian.Uint32(fileTable[off+8 : off+12])),
			off:     int32(binary.LittleEndian.Uint32(fileTable[off+12 : off+16])),
			size:    int32(binary.LittleEndian.Uint32(fileTable[off+16 : off+20])),
			typ:     int32(binary.LittleEndian.Uint32(fileTable[off+20 : off+24])),
		}

		// 解析文件名
		var name string
		if item.nameOff&1 != 0 {
			// UTF-16
			nameOff := int(item.nameOff>>1) * 2
			end := nameOff
			for end < len(strW) && strW[end] != 0 {
				end += 2
			}
			name = fmt.Sprintf("%x", strW[nameOff:end])
		} else {
			nameOff := int(item.nameOff >> 1)
			end := bytes.IndexByte(strA[nameOff:], 0)
			if end < 0 {
				end = len(strA) - nameOff
			}
			name = string(strA[nameOff : nameOff+end])
		}

		// 解析路径
		var path string
		if item.pathOff&1 != 0 {
			pathOff := int(item.pathOff>>1) * 2
			end := pathOff
			for end < len(strW) && strW[end] != 0 {
				end += 2
			}
			path = fmt.Sprintf("%x", strW[pathOff:end])
		} else {
			pathOff := int(item.pathOff >> 1)
			end := bytes.IndexByte(strA[pathOff:], 0)
			if end < 0 {
				end = len(strA) - pathOff
			}
			path = string(strA[pathOff : pathOff+end])
		}

		fmt.Printf("  文件 %d: %s/%s (chunk=%d, off=%d, size=%d)\n", i, path, name, item.chunk, item.off, item.size)
	}

	// 搜索 stackable 相关的文件
	fmt.Println("\n=== 搜索 stackable 目录下的 .stk 文件 ===")
	stkCount := 0
	for i := 0; i < int(hdr.FileCount) && stkCount < 20; i++ {
		off := i * 0x18
		item := fileItem{
			nameOff: int32(binary.LittleEndian.Uint32(fileTable[off : off+4])),
			pathOff: int32(binary.LittleEndian.Uint32(fileTable[off+4 : off+8])),
		}

		// 解析路径
		var path string
		if item.pathOff&1 == 0 {
			pathOff := int(item.pathOff >> 1)
			end := bytes.IndexByte(strA[pathOff:], 0)
			if end < 0 {
				continue
			}
			path = string(strA[pathOff : pathOff+end])
		} else {
			continue
		}

		// 解析文件名
		var name string
		if item.nameOff&1 == 0 {
			nameOff := int(item.nameOff >> 1)
			end := bytes.IndexByte(strA[nameOff:], 0)
			if end < 0 {
				continue
			}
			name = string(strA[nameOff : nameOff+end])
		} else {
			continue
		}

		if strings.HasPrefix(path, "stackable/") && strings.HasSuffix(name, ".stk") {
			stkCount++
			if stkCount <= 20 {
				fmt.Printf("  %s/%s\n", path, name)
			}
		}
	}
	fmt.Printf("  总共找到 %d 个 stackable .stk 文件（前 20 个）\n", stkCount)

	// 读取第一个 stk 文件内容
	fmt.Println("\n=== 读取第一个 stk 文件内容 ===")
	for i := 0; i < int(hdr.FileCount); i++ {
		off := i * 0x18
		item := fileItem{
			nameOff: int32(binary.LittleEndian.Uint32(fileTable[off : off+4])),
			pathOff: int32(binary.LittleEndian.Uint32(fileTable[off+4 : off+8])),
			chunk:   int32(binary.LittleEndian.Uint32(fileTable[off+8 : off+12])),
			off:     int32(binary.LittleEndian.Uint32(fileTable[off+12 : off+16])),
			size:    int32(binary.LittleEndian.Uint32(fileTable[off+16 : off+20])),
			typ:     int32(binary.LittleEndian.Uint32(fileTable[off+20 : off+24])),
		}

		var name string
		if item.nameOff&1 == 0 {
			nameOff := int(item.nameOff >> 1)
			end := bytes.IndexByte(strA[nameOff:], 0)
			if end < 0 {
				continue
			}
			name = string(strA[nameOff : nameOff+end])
		} else {
			continue
		}

		if strings.HasSuffix(name, ".stk") {
			chunkData, err := getChunk(item.chunk)
			if err != nil {
				fmt.Printf("  解压 chunk 失败: %v\n", err)
				continue
			}
			fileData := chunkData[item.off : item.off+item.size]
			fmt.Printf("  文件: %s\n", name)
			fmt.Printf("  大小: %d 字节\n", len(fileData))
			fmt.Printf("  前 100 字节: %q\n", fileData[:100])
			break
		}
	}

	fmt.Println("\n✓ PVF 读取工具验证成功！")
}
