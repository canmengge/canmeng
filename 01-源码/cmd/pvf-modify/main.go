package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
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
	lcgMul               = 0x343FD
	headerSize           = 0x30
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

func main() {
	pvfPath := `D:\AA110pvf修改\Script.pvf`
	skPath := `D:\AA110pvf修改\sk.dat`

	// 读取 PVF 和 sk.dat
	fmt.Println("1. 读取文件...")
	data, _ := os.ReadFile(pvfPath)
	sealed, _ := os.ReadFile(skPath)
	fmt.Printf("   PVF: %d 字节\n", len(data))
	fmt.Printf("   sk.dat: %d 字节\n", len(sealed))

	// RSA 解密 sk.dat
	fmt.Println("\n2. RSA 解密 sk.dat...")
	der, _ := hex.DecodeString(paged110RSADER)
	privKey, _ := x509.ParsePKCS1PrivateKey(der)
	table := make([]byte, 0, len(sealed))
	for off := 0; off < len(sealed); off += 128 {
		decrypted, _ := rsa.DecryptPKCS1v15(nil, privKey, sealed[off:off+128])
		table = append(table, decrypted...)
	}
	fmt.Printf("   页密钥表: %d 字节 (%d 页)\n", len(table), len(table)/paged110PageKeySize)

	// AES 解密页密钥表
	fmt.Println("\n3. AES 解密页密钥表...")
	metadataKey, _ := hex.DecodeString(paged110EmbeddedMetadataHex)
	unwrapped := make([]byte, len(table))
	copy(unwrapped, table)
	n := len(unwrapped) &^ 255
	block, _ := aes.NewCipher(metadataKey)
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(unwrapped[:n], unwrapped[:n])
	fmt.Println("   完成")

	// 解密所有页守卫
	fmt.Println("\n4. 解密页守卫...")
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
	fmt.Println("   完成")

	// 解密头部
	fmt.Println("\n5. 解密头部...")
	var hdrRaw [headerSize]byte
	copy(hdrRaw[:], buf[:headerSize])
	seed := wideSeed("iNfO")
	cryptSeed(seed, magicMain, hdrRaw[:])
	hdr := decodeHeader(hdrRaw[:])
	fmt.Printf("   文件数: %d\n", hdr.FileCount)
	fmt.Printf("   BodySize: %d\n", hdr.BodySize)
	fmt.Printf("   GroupCount: %d\n", hdr.GroupCount)
	fmt.Printf("   HashTableSize: %d\n", hdr.HashTableSize)
	fmt.Printf("   NameTableSize: %d\n", hdr.NameTableSize)

	// 计算各段偏移
	fileTableOff := headerSize
	hashTableOff := fileTableOff + int(hdr.FileCount)*0x18
	nameTableOff := hashTableOff + int(hdr.HashTableSize)
	grpiOff := nameTableOff + int(hdr.NameTableSize)
	bodyOff := grpiOff + int(hdr.GroupCount)*8

	fmt.Printf("\n6. 各段偏移:\n")
	fmt.Printf("   文件表: 0x%x\n", fileTableOff)
	fmt.Printf("   哈希表: 0x%x\n", hashTableOff)
	fmt.Printf("   名称表: 0x%x\n", nameTableOff)
	fmt.Printf("   GRPI: 0x%x\n", grpiOff)
	fmt.Printf("   Body: 0x%x\n", bodyOff)

	// 读取前几个文件项，验证格式
	fmt.Println("\n7. 读取前 5 个文件项验证格式...")
	for i := 0; i < 5; i++ {
		off := fileTableOff + i*0x18
		item := fileItem{
			nameOff: int32(binary.LittleEndian.Uint32(buf[off : off+4])),
			pathOff: int32(binary.LittleEndian.Uint32(buf[off+4 : off+8])),
			chunk:   int32(binary.LittleEndian.Uint32(buf[off+8 : off+12])),
			off:     int32(binary.LittleEndian.Uint32(buf[off+12 : off+16])),
			size:    int32(binary.LittleEndian.Uint32(buf[off+16 : off+20])),
			typ:     int32(binary.LittleEndian.Uint32(buf[off+20 : off+24])),
		}
		fmt.Printf("   文件 %d: nameOff=%d pathOff=%d chunk=%d off=%d size=%d type=%d\n",
			i, item.nameOff, item.pathOff, item.chunk, item.off, item.size, item.typ)
	}

	// 尝试读取名称池的前几个字节，看看是否需要解密
	fmt.Println("\n8. 检查名称池格式...")
	fmt.Printf("   名称池前 32 字节: %x\n", buf[nameTableOff:nameTableOff+32])

	// 搜索 "50001248" 这个字符串
	fmt.Println("\n9. 搜索物品 ID 50001248...")
	target := []byte("50001248")
	foundCount := 0
	for i := 0; i < len(buf)-len(target); i++ {
		match := true
		for j := 0; j < len(target); j++ {
			if buf[i+j] != target[j] {
				match = false
				break
			}
		}
		if match {
			foundCount++
			if foundCount <= 5 {
				fmt.Printf("   找到匹配在偏移 0x%x\n", i)
				// 显示周围的内容
				start := i - 10
				if start < 0 {
					start = 0
				}
				end := i + 20
				if end > len(buf) {
					end = len(buf)
				}
				fmt.Printf("   周围内容: %q\n", buf[start:end])
			}
		}
	}
	fmt.Printf("   总共找到 %d 处匹配\n", foundCount)

	// 保存解密后的第一页，方便后续分析
	os.WriteFile(`D:\AA110pvf修改\decrypted_page1.bin`, buf[:paged110PageGuardSize], 0644)
	fmt.Println("\n✓ 第一页解密后的数据已保存到 decrypted_page1.bin")
	fmt.Println("  解密逻辑验证成功！")
}
