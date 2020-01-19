package util

import (
	"bytes"
	"encoding/binary"
	"fmt"
	rand2 "math/rand"
	"time"
)

func init() {
	rand2.Seed(time.Now().UnixNano())
}

//随机数[0,n)
func Random(n int) int {
	return int(rand2.Int31n(int32(n)))
}

//时间戳毫秒
func TimeStamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

//指定日期的时间戳毫秒
func TimeStampTarget(y int, m time.Month, d int, h int, mt int, s int) int64 {
	return time.Date(y, m, d, h, mt, s, 0, time.Local).UnixNano() / int64(time.Millisecond)
}

func CheckErr(err error) bool {
	if err != nil {
		fmt.Println("CheckErr: ", err)
		return true
	}
	return false
}

func BytesToUInt16(buff []byte, order binary.ByteOrder) uint16 {
	bytebuff := bytes.NewBuffer(buff)
	var data uint16
	binary.Read(bytebuff, order, &data)
	return data
}