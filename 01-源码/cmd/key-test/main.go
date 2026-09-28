package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/x509"
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

func main() {
	skPath := `D:\AA110pvf修改\sk.dat`

	// 1. 加载 RSA 私钥
	fmt.Println("1. 加载 RSA 私钥...")
	der, err := hex.DecodeString(paged110RSADER)
	if err != nil {
		log.Fatalf("解码 DER 失败: %v", err)
	}
	privKey, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		log.Fatalf("解析 RSA 私钥失败: %v", err)
	}
	fmt.Printf("   RSA 私钥加载成功，模数长度: %d 位\n", privKey.N.BitLen())

	// 2. 读取 sk.dat
	fmt.Println("\n2. 读取 sk.dat...")
	sealed, err := os.ReadFile(skPath)
	if err != nil {
		log.Fatalf("读取 sk.dat 失败: %v", err)
	}
	fmt.Printf("   sk.dat 大小: %d 字节 (%d 个块)\n", len(sealed), len(sealed)/128)

	// 3. RSA 解密每个块
	fmt.Println("\n3. RSA 解密 sk.dat...")
	out := make([]byte, 0, len(sealed))
	successCount := 0
	failCount := 0
	for off := 0; off < len(sealed); off += 128 {
		block := sealed[off : off+128]
		decrypted, err := rsa.DecryptPKCS1v15(nil, privKey, block)
		if err != nil {
			failCount++
			fmt.Printf("   块 %d 解密失败: %v\n", off/128, err)
			continue
		}
		successCount++
		out = append(out, decrypted...)
	}
	fmt.Printf("   成功: %d, 失败: %d\n", successCount, failCount)

	if successCount == 0 {
		fmt.Println("\n✗ RSA 解密完全失败，说明这个 sk.dat 不是用这套密钥加密的")
		fmt.Println("  需要找到正确版本的 RSA 私钥")
		return
	}

	fmt.Printf("   解密后大小: %d 字节\n", len(out))
	fmt.Printf("   前 32 字节: %x\n", out[:32])

	// 4. 尝试用 metadata key AES 解密
	fmt.Println("\n4. 尝试用 metadata key AES 解密页密钥表...")
	metadataKey, err := hex.DecodeString(paged110EmbeddedMetadataHex)
	if err != nil {
		log.Fatalf("解码 metadata key 失败: %v", err)
	}
	fmt.Printf("   metadata key: %s\n", paged110EmbeddedMetadataHex)

	// 复制一份用来解密
	unwrapped := make([]byte, len(out))
	copy(unwrapped, out)

	block, err := aes.NewCipher(metadataKey)
	if err != nil {
		log.Fatalf("创建 AES cipher 失败: %v", err)
	}

	// CBC 解密，IV = 0
	n := len(unwrapped) &^ 255 // paged110MetadataAlign = 256
	if n == 0 {
		fmt.Println("   数据太小，无法解密")
		return
	}
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(unwrapped[:n], unwrapped[:n])

	fmt.Printf("   解密后前 32 字节: %x\n", unwrapped[:32])
	fmt.Println("\n✓ 如果上面的字节看起来像随机数据（不是乱码），说明解密成功")
	fmt.Println("  如果 RSA 成功但 AES 后看起来还是乱码，说明 metadata key 不对")
}
