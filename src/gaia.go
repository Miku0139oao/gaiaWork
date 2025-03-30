package gaiaWork

import (
	"fmt"
	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

type Employee struct {
	Name       string
	Position   string
	Schedule   map[string]string
	IsPartTime bool
	WorkHours  map[string]float64 // 新增工时存储
}

const (
	ShiftTypeA         = "A"
	ShiftTypeB         = "B"
	ShiftTypeC         = "C"
	ShiftTypeAEarlyEnd = "A-1400"
	ShiftTypeBNight    = "1800-B"
 ShiftTypeExp = "EXP"
)

var (
	nicknameMap = make(map[string]string)
	// 新增统计数据结构
	dayStats = make(map[string]struct {
		Morning int     // 返早人数
		Night   int     // 返夜人数
		Midday  int     // 返中人数
		Total   float64 // 总工时
	})
)

func init() {
	loc, _ := time.LoadLocation("Asia/Hong_Kong")
	time.Local = loc
	loadNicknames("nicknames.yaml")
}

func ProcessEmployees(rows [][]string, dates []string) ([]Employee, []Employee) {
	var fullTime []Employee
	var partTime []Employee

	for _, row := range rows {
		if len(row) < 28 || row[0] == "" {
			continue
		}

		if !hasNickname(row[0]) {
			continue
		}

		emp := parseEmployee(row, dates)

		if emp.IsPartTime {
			partTime = append(partTime, emp)
		} else {
			fullTime = append(fullTime, emp)
		}
	}
	return fullTime, partTime
}

func hasNickname(name string) bool {
	key := strings.ToUpper(strings.ReplaceAll(name, " ", ""))
	_, exists := nicknameMap[key]
	return exists
}

func parseEmployee(row []string, dates []string) Employee {
	emp := Employee{
		Name:       strings.TrimSpace(row[0]),
		Position:   strings.TrimSpace(row[1]),
		Schedule:   make(map[string]string),
		WorkHours:  make(map[string]float64),
		IsPartTime: strings.Contains(strings.ToUpper(row[1]), "PART TIME"),
	}

	for i := 2; i < 28; i++ {
		if i-2 >= len(dates) {
			break
		}
		date := dates[i-2]
		shift, hours := parseShiftDetail(row[i])
		emp.Schedule[date] = shift
		emp.WorkHours[date] = hours

		// 更新统计
		updateDailyStats(date, shift, hours)
	}
	return emp
}

func getDisplayName(original string) string {
	key := strings.ToUpper(strings.ReplaceAll(original, " ", ""))
	return nicknameMap[key]
}

func GenerateScheduleSheet(f *excelize.File, fullTime, partTime []Employee, dates []string) {
	const sheetName = "排班明細"
	f.NewSheet(sheetName)
	f.DeleteSheet("Sheet1")

	f.SetColWidth(sheetName, "A", "A", 20)
	for i := 0; i < len(dates); i++ {
		col, _ := excelize.ColumnNumberToName(i + 2) // 列索引调整
		f.SetColWidth(sheetName, col, col, 18)
	}

	headers := append([]string{"姓名"}, dates...)
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheetName, cell, h)
		f.SetCellStyle(sheetName, cell, cell, 1)
	}

	priorityPositions := map[string]int{
		"store manager":           0,
		"assistant store manager": 1,
		"store supervisor":        2,
	}

	var priorityGroup, regularGroup []Employee
	for _, emp := range fullTime {
		lowerPos := strings.ToLower(emp.Position)
		if _, exists := priorityPositions[lowerPos]; exists {
			priorityGroup = append(priorityGroup, emp)
		} else {
			regularGroup = append(regularGroup, emp)
		}
	}

	// 排序优先组
	sort.Slice(priorityGroup, func(i, j int) bool {
		posI := strings.ToLower(priorityGroup[i].Position)
		posJ := strings.ToLower(priorityGroup[j].Position)
		if priorityPositions[posI] != priorityPositions[posJ] {
			return priorityPositions[posI] < priorityPositions[posJ]
		}
		return getDisplayName(priorityGroup[i].Name) == "Wui"
	})

	rowNum := 2
	// 写入优先组
	for _, emp := range priorityGroup {
		writeEmployeeRow(f, sheetName, rowNum, emp, dates)
		rowNum++
	}

	// 添加分隔空行
	rowNum++

	// 写入其他全职员工
	for _, emp := range regularGroup {
		writeEmployeeRow(f, sheetName, rowNum, emp, dates)
		rowNum++
	}

	// 添加兼职员工分隔行
	rowNum += 2

	// 写入兼职员工
	for _, emp := range partTime {
		writeEmployeeRow(f, sheetName, rowNum, emp, dates)
		rowNum++
	}

	writeStatistics(f, sheetName, rowNum+3, dates)
}

func loadNicknames(filename string) {
	file, _ := os.ReadFile(filename)

	tempMap := make(map[string]string)
	if err := yaml.Unmarshal(file, &tempMap); err != nil {
		log.Fatal("YAML解析錯誤:", err)
	}

	for k, v := range tempMap {
		key := strings.ToUpper(strings.ReplaceAll(k, " ", ""))
		nicknameMap[key] = v
	}
}

func CreateStyles(f *excelize.File) {
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold:   true,
			Color:  "FFFFFF",
			Family: "Microsoft JhengHei",
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"2F5496"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Vertical: "center",
			WrapText: true,
		},
	})

	dataStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{
			Vertical: "center",
			WrapText: true,
		},
		Font: &excelize.Font{
			Family: "Microsoft JhengHei",
		},
	})

	f.SetCellStyle("排班明細", "A1", "Z1", headerStyle)
	f.SetCellStyle("排班明細", "A2", "Z1000", dataStyle)
}

func ParseDates(rawDates []string) []string {
	dates := make([]string, 0)
	for _, d := range rawDates {
		parts := strings.Fields(d)
		if len(parts) > 0 {
			dates = append(dates, fmt.Sprintf("%s\n%s", parts[0], parts[1]))
		}
	}
	return dates
}

func formatTimeRange(timeRange string) string {
	times := strings.Split(timeRange, "-")
	if len(times) != 2 {
		return ""
	}

	start := strings.TrimSpace(times[0])
	end := strings.TrimSpace(times[1])

	// 移除无效的00:00-00:02时间
	if start == "00:00" && end == "00:02" {
		return ""
	}

	// 统一格式化为HH:mm-HH:mm
	return fmt.Sprintf("%s-%s", start, end)
}

func writeEmployeeRow(f *excelize.File, sheetName string, rowNum int, emp Employee, dates []string) {
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), getDisplayName(emp.Name))

	// 日期列索引从B列开始（原C列）
	for colIdx, date := range dates {
		cell, _ := excelize.CoordinatesToCellName(colIdx+2, rowNum) // 从B列开始
		f.SetCellValue(sheetName, cell, emp.Schedule[date])
	}
}

// 新增时间解析函数
func parseTimeRange(timeRange string) (time.Time, time.Time, error) {
	times := strings.Split(timeRange, "-")
	if len(times) != 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid time format")
	}

	start, err := time.Parse("15:04", strings.TrimSpace(times[0]))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	end, err := time.Parse("15:04", strings.TrimSpace(times[1]))
	if err != nil {
		return start, time.Time{}, err
	}

	if end.Before(start) {
		end = end.Add(24 * time.Hour)
	}

	return start, end, nil
}

func calculateHours(timeRange string) float64 {
	start, end, err := parseTimeRange(timeRange)
	if err != nil {
		return 0
	}

	duration := end.Sub(start).Minutes()
	return math.Round(duration/60*10) / 10 // 保留1位小数
}

func parseShiftDetail(raw string) (string, float64) {

	parts := strings.SplitN(raw, " ", 2)
	var timeRange string
	if len(parts) > 1 {
		timeRange = parts[1]
	}
	switch parts[0] {
	case "OFF", "年假", "HK-PH", "HK-SH":
		return parts[0], 0
	}
	statType := classifySpecialShiftWithDetail(timeRange)
	hours := calculateHours(timeRange)
	finalDisplay := statType
	return finalDisplay, hours
}

func classifySpecialShiftWithDetail(timeRange string) (statType string) {
	start, end, err := parseTimeRange(timeRange)
	if err != nil {
		return "特定班"
	}

	startMins := start.Hour()*60 + start.Minute()
	endMins := end.Hour()*60 + end.Minute()

	// 处理跨午夜情况
	if endMins < startMins {
		endMins += 1440
	}

	switch {
	// A
	case startMins == 510 && endMins == 1080:
		return ShiftTypeA
	case startMins == 510 && endMins == 840:
		return ShiftTypeAEarlyEnd
		//

	//B
	case startMins == 810 && endMins == 1380:
		return ShiftTypeB
	case startMins == 1080 && endMins == 1380:
		return ShiftTypeBNight

	//C
	case startMins == 630 && endMins == 1200:
		return ShiftTypeC
	// 其他时段保持原样
 case startMins == 540 && endMins == 1110:
  return ShiftTypeExp
	default:
		return timeRange
	}
}

func updateDailyStats(date, shiftType string, hours float64) {
	stats := dayStats[date]

	// 解析班型前缀（按换行符分割）
	lines := strings.SplitN(shiftType, "\n", 2)
	prefix := lines[0]

	switch {
	case prefix == "A", prefix == ShiftTypeAEarlyEnd:
		stats.Morning++
	case prefix == ShiftTypeBNight, prefix == ShiftTypeB:
		stats.Night++
	case prefix == ShiftTypeC:
		stats.Midday++
	default:
		break
	}

	stats.Total += hours
	dayStats[date] = stats
}

func writeStatistics(f *excelize.File, sheetName string, startRow int, dates []string) {
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", startRow), "每日班次統計")
	// 合併單元格範圍需同步調整（原H改為G）
	f.MergeCell(sheetName, fmt.Sprintf("A%d", startRow), fmt.Sprintf("G%d", startRow))
	//f.SetCellStyle(sheetName, fmt.Sprintf("A%d", startRow), fmt.Sprintf("H%d", startRow), 1)

	writeStatRow(f, sheetName, startRow+1, "返早人數", dates, func(date string) interface{} {
		return dayStats[date].Morning
	})

	writeStatRow(f, sheetName, startRow+2, "返中人數", dates, func(date string) interface{} {
		return dayStats[date].Midday
	})

	writeStatRow(f, sheetName, startRow+3, "返夜人數", dates, func(date string) interface{} {
		return dayStats[date].Night
	})

	writeStatRow(f, sheetName, startRow+4, "實際工時", dates, func(date string) interface{} {
		return fmt.Sprintf("%.1fh", dayStats[date].Total)
	})
}

func writeStatRow(f *excelize.File, sheetName string, row int, title string, dates []string, getValue func(string) interface{}) {
	f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), title)

	// 修正列索引：從 B 列開始 (座標系索引2)
	for i := 0; i < len(dates); i++ {
		colName, _ := excelize.ColumnNumberToName(i + 2) // +2 對應B列開始
		cell := fmt.Sprintf("%s%d", colName, row)
		f.SetCellValue(sheetName, cell, getValue(dates[i]))
	}

	// 總計列處理（保持不變）
	totalCol, _ := excelize.ColumnNumberToName(len(dates) + 2)
	f.SetCellValue(sheetName, fmt.Sprintf("%s%d", totalCol, row), getValue("total"))
}
