package main

import (
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"strconv"
)

//type vmixActivatorConfig struct {
//	trigger   string
//	input     int
//	onAction  string
//	offAction string
//}

func main() {

	wb, _ := excelize.OpenFile("responses.xlsx")
	mc := make(map[string]*map[int]vmixActivatorConfig)

	activatorCols, _ := wb.GetCols("Activators")

	for i, col := range activatorCols {
		if i > 0 && col != nil {
			var onAction string
			var offAction string
			var trigger string
			var input int
			//col = truncateSlice(col)
			//read the column in chunks of 3 lines, create a vmixActivatorConsole with the info, and
			//add to the inputMap for that trigger
			trigger = col[0]
			inputMap := make(map[int]vmixActivatorConfig)
			for i := 1; col[i] != ""; i = i + 3 {

				input, _ = strconv.Atoi(col[i])
				onAction = col[i+1]
				offAction = col[i+2]
				vmc := new(vmixActivatorConfig)
				vmc.trigger = trigger
				vmc.input = input
				vmc.onAction = onAction
				vmc.offAction = offAction
				inputMap[input] = *vmc
			}
			mc[trigger] = &inputMap

		}
	}
	vc := mc["Streaming"]
	fmt.Println(*vc)
}
