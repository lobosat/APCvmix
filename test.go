package main

import (
	//"bytes"
	"fmt"
	//"github.com/davecgh/go-spew/spew"
	//"io/ioutil"
	//"strconv"
	//"sync"
	//"time"

	//"gitlab.com/gomidi/midi"
	//"gitlab.com/gomidi/midi/midimessage/meta"
	//"gitlab.com/gomidi/midi/midimessage/meta/meter"
	//"gitlab.com/gomidi/midi/midireader"
	//"gitlab.com/gomidi/midi/smf"
	//"gitlab.com/gomidi/midi/smf/smfwriter"
	//"gitlab.com/gomidi/midi/writer"

	// replace with e.g. "gitlab.com/gomidi/rtmididrv" for real midi connections
	driver "gitlab.com/gomidi/rtmididrv"
)

type timedMsg struct {
	deltaMicrosecs int64
	data           []byte
}

func main() {
	drv, _ := driver.New()
	defer drv.Close()
	ins, _ := drv.Ins()
	outs, _ := drv.Outs()
	inPort := ins[1]
	outPort := outs[1]

	defer inPort.Close()
	defer outPort.Close()

	// here comes the meat
	//
	//var inbf bytes.Buffer
	//var outbf bytes.Buffer
	ch := make(chan timedMsg)
	inPort.SetListener(func(data []byte, deltaMicrosecs int64) {
		//if len(data) == 0 {
		//	return
		//}
		ch <- timedMsg{data: data, deltaMicrosecs: deltaMicrosecs}
		fmt.Println(data)
	})
	//var wg sync.WaitGroup
	//wg.Add(1)
	//wg.Wait()
	for {
		msg := <-ch
		fmt.Println(msg)
	}

}
