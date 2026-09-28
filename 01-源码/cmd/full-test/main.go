package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
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
	paged110PageKeySize   = 32
)

func main() {
	pvfPath := `D:\AA110pvf修改\Script.pvf`
	skPath := `D:\AA110pvf修改\sk.dat`

	// 1. 读取 PVF
	fmt.Println("1. 读取 PVF 文件...")
	data, err := os.ReadFile(pvfPath)
	if err != nil {
		log.Fatalf("读取 PVF 失败: %v", err)
	}
	fmt.Printf("   PVF 大小: %d 字节 (%.1f MB)\n", len(data), float64(len(data))/1024/1024)

	// 2. 读取并解密 sk.dat
	fmt.Println("\n2. 读取并 RSA 解密 sk.dat...")
	sealed, err := os.ReadFile(skPath)
	if err != nil {
		log.Fatalf("读取 sk.dat 失败: %v", err)
	}
	der, _ := hex.DecodeString(paged110RSADER)
	privKey, _ := x509.ParsePKCS1PrivateKey(der)
	table := make([]byte, 0, len(sealed))
	for off := 0; off < len(sealed); off += 128 {
		decrypted, err := rsa.DecryptPKCS1v15(nil, privKey, sealed[off:off+128])
		if err != nil {
			log.Fatalf("RSA 解密失败: %v", err)
		}
		table = append(table, decrypted...)
	}
	fmt.Printf("   RSA 解密成功，页密钥表大小: %d 字节\n", len(table))
	fmt.Printf("   页数: %d (每页 %d 字节密钥)\n", len(table)/paged110PageKeySize, paged110PageKeySize)

	// 3. 用 metadata key AES 解密页密钥表
	fmt.Println("\n3. 用 metadata key AES 解密页密钥表...")
	metadataKey, _ := hex.DecodeString(paged110EmbeddedMetadataHex)
	unwrapped := make([]byte, len(table))
	copy(unwrapped, table)
	n := len(unwrapped) &^ 255
	if n == 0 {
		log.Fatal("页密钥表太小")
	}
	block, err := aes.NewCipher(metadataKey)
	if err != nil {
		log.Fatalf("创建 AES cipher 失败: %v", err)
	}
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(unwrapped[:n], unwrapped[:n])
	fmt.Printf("   AES 解密完成\n")
	fmt.Printf("   前 16 字节: %x\n", unwrapped[:16])

	// 4. 解密页守卫
	fmt.Println("\n4. 解密页守卫 (前 10240 字节)...")
	buf := make([]byte, len(data))
	copy(buf, data)
	pages := len(unwrapped) / paged110PageKeySize
	done := 0
	for i := 0; i < pages; i++ {
		off := i * paged110PageSize
		if off+paged110PageGuardSize > len(buf) {
			break
		}
		pageBlock, err := aes.NewCipher(unwrapped[i*paged110PageKeySize : (i+1)*paged110PageKeySize])
		if err != nil {
			break
		}
		cipher.NewCBCDecrypter(pageBlock, make([]byte, aes.BlockSize)).
			CryptBlocks(buf[off:off+paged110PageGuardSize], buf[off:off+paged110PageGuardSize])
		done++
	}
	fmt.Printf("   成功解密 %d 页的守卫\n", done)

	// 5. 检查头部
	fmt.Println("\n5. 检查头部...")
	var hdrRaw [0x30]byte
	copy(hdrRaw[:], buf[:0x30])

	// 尝试解密头部 (iNfO key)
	// wideSeed("iNfO") 的值，我们先直接看原始字节
	fmt.Printf("   头部前 16 字节 (解密后): %x\n", hdrRaw[:16])

	// 检查 nkpi 签名 (MagicSignature = 0x69706b6e)
	magic := binary.LittleEndian.Uint32(hdrRaw[:4])
	fmt.Printf("   前 4 字节作为 uint32: 0x%x\n", magic)
	fmt.Printf("   期望的 nkpi 签名: 0x%x\n", uint32(0x69706b6e))

	if magic == 0x69706b6e {
		fmt.Println("\n✓ 头部签名验证成功！")
	} else {
		fmt.Println("\n✗ 头部签名不对，说明头部密钥也不对")
		fmt.Println("  需要找到正确的头部解密密钥")
	}

	// 6. 看看第一页守卫解密后的内容
	fmt.Println("\n6. 第一页守卫前 64 字节:")
	fmt.Printf("   原始: %x\n", data[:64])
	fmt.Printf("   解密后: %x\n", buf[:64])
}
