package main

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var RequireFont = map[string]bool{
	// fyne 显示中文错误 所以需要修改字体为以下字体
	"simhei.ttf": false, // 黑体
	"Deng.ttf":   false, // 等线家族
	"Dengb.ttf":  false,
	"Dengl.ttf":  false,
}

const FontPath = "C:\\Windows\\Fonts"

var SD2DModPath = ""
var SD2DMapPath = ""

//go:embed assets/main.ico
var iconData []byte

func initFont() {
	// 初始化字体
	files, err := os.ReadDir(FontPath)
	if err != nil {
		panic(err.Error())
	}
	for _, file := range files {
		fileInfo, _ := file.Info()
		// 是文件夹跳过
		if !fileInfo.IsDir() {
			fileName := fileInfo.Name()
			// 符合字体文件要求
			if strings.Contains(fileName, ".ttf") {
				for k, _ := range RequireFont {
					if k == fileName {
						RequireFont[k] = true
						return
					}
				}
			}
		}
	}
}

type CustomTheme struct {
	font []byte
}

func (t *CustomTheme) Font(s fyne.TextStyle) fyne.Resource {
	return &fyne.StaticResource{
		StaticName:    "CustomFont",
		StaticContent: t.font,
	}
}

func (t *CustomTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(n, v)
}

func (t *CustomTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (t *CustomTheme) Size(n fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(n)
}

func DownloadEvent(DownloadButton *widget.Button, DownloadBar *widget.ProgressBar, WelcomeText *canvas.Text) {
	DownloadBar.Show()
	DownloadButton.Disable()
	DownloadButton.SetText("下载中...")
	go Download(DownloadButton, DownloadBar, WelcomeText)
}

func Download(DownloadButton *widget.Button, DownloadBar *widget.ProgressBar, WelcomeText *canvas.Text) {
	slog.Info("下载中")
	Mods := getFileList()
	modCount := len(Mods)

	go func() {
		for i, mod := range Mods {
			if mod.Type == 1 {
				continue
			}
			slog.Info("正在下载: ", mod.FileName, mod.Size)
			size := float64(mod.Size) / 1024 / 1024 / 1024
			WelcomeText.Text = fmt.Sprintf("正在下载: %s 大小: %.2f GB %d/%d", mod.FileName, size, i, modCount)
			WelcomeText.Refresh()
			DownloadBar.SetValue(0)
			DownloadBar.Max = float64(mod.Size)
			downloadMod(mod, DownloadBar)
		}
		WelcomeText.Text = "下载完成"
		WelcomeText.TextSize = 25
		WelcomeText.Refresh()
		DownloadButton.Hide()
	}()

	DownloadButton.OnTapped = func() {
		DownloadEvent(DownloadButton, DownloadBar, WelcomeText)
	}
}

func downloadMod(mod Mod, DownloadBar *widget.ProgressBar) {
	const maxRetries = 100
	var retries int

	for retries < maxRetries {
		err := attemptDownload(mod, DownloadBar)
		if err == nil {
			return
		}
		retries++
		fmt.Printf("Retrying download (%d/%d)\n", retries, maxRetries)
		time.Sleep(time.Second * 2) // 等待一段时间后重试
	}
	fmt.Println("Failed to download after multiple attempts")
}

func decodeFileName(s string) (string, error) {
	dec := simplifiedchinese.GBK.NewDecoder()
	reader := transform.NewReader(bytes.NewReader([]byte(s)), dec)
	result, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// Unzip 解压缩函数
func Unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// 检测和转换文件名编码
		fileName, err := decodeFileName(f.Name)
		if err != nil {
			return err
		}

		// 创建文件的输出路径
		outputPath := filepath.Join(dest, fileName)
		if f.FileInfo().IsDir() {
			// 如果是目录则创建目录
			os.MkdirAll(outputPath, os.ModePerm)
		} else {
			// 如果是文件则创建文件
			if err := os.MkdirAll(filepath.Dir(outputPath), os.ModePerm); err != nil {
				return err
			}
			outFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				return err
			}
			_, err = io.Copy(outFile, rc)
			outFile.Close()
			rc.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func attemptDownload(mod Mod, DownloadBar *widget.ProgressBar) error {
	tmpFileName := mod.FileName + ".tmp"
	var file *os.File
	var err error
	var startPos int64 = 0

	if _, err = os.Stat(tmpFileName); os.IsNotExist(err) {
		file, err = os.Create(tmpFileName)
		if err != nil {
			fmt.Println("Error creating file:", err)
			return err
		}
	} else {
		file, err = os.OpenFile(tmpFileName, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Println("Error opening file:", err)
			return err
		}
		stat, err := file.Stat()
		if err != nil {
			fmt.Println("Error getting file stats:", err)
			return err
		}
		startPos = stat.Size()
	}
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(file.Name())
		} else {
			os.Rename(file.Name(), mod.FileName)
			if mod.Type == 0 {
				Unzip(mod.FileName, SD2DModPath)
			} else if mod.Type == 2 {
				Unzip(mod.FileName, SD2DMapPath)
			}
		}
	}()

	client := &http.Client{}
	req, err := http.NewRequest("GET", mod.Url, nil)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startPos))

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error sending request:", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			fmt.Println("Server does not support resuming downloads, restarting from beginning")
			file.Close()
			os.Remove(tmpFileName)
			return attemptDownload(mod, DownloadBar)
		} else {
			fmt.Println("Unexpected server response:", resp.Status)
			return fmt.Errorf("unexpected server response: %s", resp.Status)
		}
	}

	go func() {
		for {
			time.Sleep(time.Second * 1)
			stat, err := file.Stat()
			if err != nil {
				return
			}
			size := stat.Size()
			DownloadBar.SetValue(float64(size))
		}
	}()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		fmt.Println("Error saving file:", err)
		return err
	}

	return nil
}

func DownloadMap(DownloadButton *widget.Button, DownloadBar *widget.ProgressBar, WelcomeText *canvas.Text) {
	mod := Mod{
		FileName: "GeneratedWorlds.zip",
		Url:      "https://cdn1.d5v.cc/CDN/File/GeneratedWorlds.zip",
		Size:     2526389975,
		Type:     2,
	}
	DownloadBar.Show()
	DownloadBar.Max = float64(mod.Size)
	DownloadBar.Refresh()
	WelcomeText.Text = "正在下载地图"
	DownloadButton.Disable()
	DownloadButton.SetText("下载中...")
	go func() {
		err := attemptDownload(mod, DownloadBar)
		if err != nil {
			fmt.Println("Error downloading map:", err)
			return
		}
		WelcomeText.Text = "下载完成"
		WelcomeText.TextSize = 25
		WelcomeText.Refresh()
		DownloadButton.Hide()
		DownloadBar.Hide()
	}()
}

func main() {
	a := app.New()
	icon := fyne.NewStaticResource("main.ico", iconData)
	a.SetIcon(icon)

	// 初始化字体
	{
		initFont()
		useFont := ""
		fontsStatus := false
		for k, v := range RequireFont {
			if !v {
				fontsStatus = true
				useFont = k
				break
			}
		}
		if !fontsStatus {
			panic("未找到所需字体 你电脑没有默认的等线和黑体？？？")
		}
		fontFile, err := os.Open(FontPath + string(os.PathSeparator) + useFont)
		if err != nil {
			panic(err.Error())
		}
		font, err := io.ReadAll(fontFile)
		if err != nil {
			panic(err.Error())
		}

		customTheme := CustomTheme{font: font}
		a.Settings().SetTheme(&customTheme)
	}

	w := a.NewWindow("服务器Mod一键下载工具")

	// main
	{
		WelComeText := canvas.NewText("欢迎使用", nil)
		WelComeText.TextSize = 20
		WelComeText.Refresh()

		DownloadBar := widget.NewProgressBar()
		DownloadBar.Hide()

		DownloadButton := widget.NewButton("开始下载", func() {})
		OpenModDir := widget.NewButton("打开Mod目录", func() {
			cmd := exec.Command("explorer.exe", SD2DModPath)
			err := cmd.Start()
			if err != nil {
				fmt.Println("Error:", err)
			}
		})
		DownloadMapButton := widget.NewButton("下载地图", func() {})
		DownloadMapButton.OnTapped = func() {
			DownloadMap(DownloadMapButton, DownloadBar, WelComeText)
		}

		OpenMapDir := widget.NewButton("打开地图目录", func() {
			cmd := exec.Command("explorer.exe", SD2DMapPath)
			err := cmd.Start()
			if err != nil {
				fmt.Println("Error:", err)
			}
		})
		DownloadButton.OnTapped = func() {
			DownloadEvent(DownloadButton, DownloadBar, WelComeText)
		}

		w.SetContent(container.NewVBox(
			layout.NewSpacer(),
			container.NewHBox(layout.NewSpacer(), WelComeText, layout.NewSpacer()),
			layout.NewSpacer(),
			container.NewHBox(layout.NewSpacer(), DownloadButton, DownloadMapButton, OpenModDir, OpenMapDir, layout.NewSpacer()),
			layout.NewSpacer(),
			DownloadBar,
		))

		w.Resize(fyne.NewSize(600, 400))
		w.SetFixedSize(true)
		w.SetIcon(a.Icon())

		w.ShowAndRun()

	}
	defer func() {
		a.Quit()
	}()

}

type Mod struct {
	FileName string `json:"FileName"`
	Url      string `json:"Url"`
	Size     int    `json:"Size"`
	Type     int    `json:"Type"`
}

func getFileList() []Mod {
	baseAPI := "https://cdn1.d5v.cc/CDN/%E5%B0%8F%E7%95%AA%E8%8C%84%E7%9A%84%E6%95%B4%E5%90%88%E5%8C%85/"
	api := baseAPI + "requirement.json"
	get, err := http.Get(api)
	if err != nil {
		panic(err.Error())
	}
	response, err := io.ReadAll(get.Body)
	var Mods []Mod
	err = json.Unmarshal(response, &Mods)
	if err != nil {
		panic(err.Error())
	}
	for i, mod := range Mods {
		mod.Url = baseAPI + url.PathEscape(mod.FileName)
		fmt.Println(mod.Url)

		Mods[i] = mod
	}

	return Mods
}

func init() {
	disks := []string{"C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M"}
	for _, disk := range disks {

		if _, err := os.Stat(disk + `:\SteamLibrary\steamapps\common\7 Days To Die\`); err == nil {
			SD2DModPath = disk + `:\SteamLibrary\steamapps\common\7 Days To Die\`
			_ = os.Mkdir(SD2DModPath+"Mods", os.ModePerm)
			SD2DMapPath = SD2DModPath + `Data\Worlds`
			SD2DModPath += "Mods"
			break
		}
	}
}
