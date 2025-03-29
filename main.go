package main

import (
	"embed"
	"encoding/json"
	"fmt"
	gaiaWork "gaia/src"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	uploadDir     = "./uploads"
	processedDir  = "./processed"
	maxUploadSize = 32 << 20 // 32MB
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type PageData struct {
	Title       string
	Message     string
	DownloadURL string
	IsError     bool
}

var templates *template.Template

func main() {
	// 初始化模板
	var err error

	templates, err = template.ParseFS(templateFS, "templates/*.html")
	templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))
	//templates, err = template.New("base.html").
	//	Funcs(template.FuncMap{
	//		"formatDate": func(t time.Time) string {
	//			return t.Format("2006-01-02")
	//		},
	//	}).
	//	ParseFS(templateFS,
	//		"templates/base.html",
	//		"templates/index.html",
	//		"templates/error.html",
	//	)
	if err != nil {
		log.Fatalf("模板解析失敗：%v", err)
	}

	// 模板驗證
	verifyTemplates := []string{"base.html", "index.html", "error.html"}
	for _, tmpl := range verifyTemplates {
		if templates.Lookup(tmpl) == nil {
			log.Fatalf("關鍵模板缺失: %s", tmpl)
		}
	}
	log.Println("所有必需模板已正確載入")

	// 創建必要目錄
	if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	}
	if err := os.MkdirAll(processedDir, os.ModePerm); err != nil {
		log.Fatalf("Failed to create processed directory: %v", err)
	}

	// 開發模式熱重載
	if os.Getenv("ENV") == "development" {
		log.Println("Running in development mode with template hot reload")
		go watchTemplates()
	}

	// 註冊路由

	http.HandleFunc("/debug/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")

		// 遍历嵌入文件
		fs.WalkDir(staticFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(w, "Walk error: %v\n", err)
				return err
			}

			info, _ := d.Info()
			fmt.Fprintf(w, "%-30s %10d bytes\n",
				path,
				info.Size())
			return nil
		})
	})

	// 创建子文件系统
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal("无法创建子文件系统:", err)
	}
	http.Handle("/static/", http.StripPrefix("/static/",
		http.FileServer(http.FS(staticSubFS))))
	http.Handle("/", recoveryMiddleware(http.HandlerFunc(indexHandler)))
	http.Handle("/index", recoveryMiddleware(http.HandlerFunc(indexHandler)))
	http.Handle("/upload", recoveryMiddleware(http.HandlerFunc(uploadHandler)))
	http.Handle("/download/", recoveryMiddleware(http.HandlerFunc(downloadHandler)))
	http.HandleFunc("/debug/static-test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
        <link href="/static/style.css" rel="stylesheet">
        <h1>靜態文件測試</h1>
        <p>如果此頁面有樣式，表示靜態文件加載正常</p>
    `))
	})
	log.Println("Server started on :7777")
	if err := http.ListenAndServe(":7777", nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				const stackSize = 4096
				stack := make([]byte, stackSize)
				length := runtime.Stack(stack, false)
				log.Printf("[PANIC RECOVERED] %v\n%s", err, stack[:length])

				renderTemplate(w, "error.html", PageData{
					Title:   "系統異常",
					Message: fmt.Sprintf("錯誤類型: %T\n詳細訊息: %v", err, err),
					IsError: true,
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// 模板熱重載監視
func watchTemplates() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if _, err := template.ParseFS(templateFS, "templates/*.html"); err == nil {
			templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))
			log.Println("Templates reloaded successfully")
		}
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("處理首頁請求 - 來源IP: %s | 用戶代理: %s",
		r.RemoteAddr,
		r.UserAgent(),
	)

	data := PageData{
		Title: "GAIA 更表處理器",
	}
	renderTemplate(w, "index.html", data)
}

func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// 打印调试信息
	log.Printf("正在渲染模板: %s (可用模板: %v)", tmpl, templates.DefinedTemplates())

	// 检查模板是否存在
	if templates.Lookup(tmpl) == nil {
		log.Printf("模板 %s 不存在", tmpl)
		http.Error(w, "页面不存在", http.StatusInternalServerError)
		return
	}

	// 检查基础模板
	if templates.Lookup("base.html") == nil {
		log.Fatal("基础模板 base.html 未找到")
	}

	err := templates.ExecuteTemplate(w, "base.html", data)
	if err != nil {
		log.Printf("模板渲染错误详情: %v", err)
		http.Error(w, "内部服务器错误", http.StatusInternalServerError)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// 1. 解析表单
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 限制 10MB
		writeJSONError(w, "文件过大或表单解析失败", http.StatusBadRequest)
		return
	}

	// 2. 获取上传的文件
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, "读取文件失败", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 3. 保存文件到临时目录
	tempFilePath := "./uploads/" + header.Filename
	dst, err := os.Create(tempFilePath)
	if err != nil {
		writeJSONError(w, "无法创建临时文件", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		writeJSONError(w, "保存文件失败", http.StatusInternalServerError)
		return
	}

	// 4. 处理文件（假设处理后生成 processed.xlsx）

	filePath, err := processFile(tempFilePath)

	// 5. 返回处理后的文件（不设置其他响应头！）
	w.Header().Set("Content-Disposition", "attachment; filename="+filePath)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filePath)
}

func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	fileName := filepath.Base(r.URL.Path)
	if fileName == "." || fileName == "/" {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(processedDir, fileName)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+fileName)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filePath)
}

func processFile(inputPath string) (string, error) {
	// 初始化 Excel 文件
	f := excelize.NewFile()
	defer f.Close()

	// 生成文件名
	outputName := uuid.New().String() + ".xlsx"
	outputPath := filepath.Join(processedDir, outputName)

	// 建立樣式
	gaiaWork.CreateStyles(f)

	// 讀取原始文件
	srcFile, err := excelize.OpenFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	// 處理數據
	rows, err := srcFile.GetRows("排班表")
	if err != nil {
		return "", fmt.Errorf("failed to read worksheet: %w", err)
	}

	dates := gaiaWork.ParseDates(rows[8][2:28])
	fullTime, partTime := gaiaWork.ProcessEmployees(rows[9:len(rows)-2], dates)

	// 生成新排班表
	gaiaWork.GenerateScheduleSheet(f, fullTime, partTime, dates)

	// 保存文件
	if err := f.SaveAs(outputPath); err != nil {
		return "", fmt.Errorf("failed to save processed file: %w", err)
	}

	return outputPath, nil
}
