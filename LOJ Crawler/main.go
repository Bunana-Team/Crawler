package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ------------------ 配置 ----------------------

const Token = "Bearer -------"

const (
	StartID      = 1
	EndID        = 533
	OutputFolder = `E:\gopachong\output25`
	YAMLPath     = OutputFolder + `\problems_version_1.0.yaml`
	BaseAPI      = "https://api.loj.ac/api"
	AssetsFolder = OutputFolder + `\assets` // 静态资源根目录
)

// ------------------ API 数据结构 ----------------------

type GetProblemRequest struct {
	DisplayId               int    `json:"displayId"`
	LocalizedContentsLocale string `json:"localizedContentsOfLocale"`
	JudgeInfo               bool   `json:"judgeInfo"`
	JudgePre                bool   `json:"judgeInfoToBePreprocessed"`
	TagsLocale              string `json:"tagsOfLocale"`
	Samples                 bool   `json:"samples"`
}

type DownloadReq struct {
	ProblemId    int      `json:"problemId"`
	Type         string   `json:"type"` // TestData / AdditionalFile
	FilenameList []string `json:"filenameList"`
}

type DownloadResp struct {
	DownloadInfo []struct {
		Filename    string `json:"filename"`
		DownloadUrl string `json:"downloadUrl"`
	} `json:"downloadInfo"`
}

type Problem struct {
	Meta struct {
		ID        int `json:"id"`
		DisplayID int `json:"displayId"`
	} `json:"meta"`
	LocalizedContentsOfLocale map[string]interface{} `json:"localizedContentsOfLocale"`
	JudgeInfo                 interface{}            `json:"judgeInfo"`
	Samples                   interface{}            `json:"samples"`
	TagsOfLocale              interface{}            `json:"tagsOfLocale"`
}

// 目标YAML结构（纯数据结构）
type TargetYAML struct {
	Version  string          `yaml:"version"`
	Problems []TargetProblem `yaml:"problems"`
}

type TargetProblem struct {
	ID           string   `yaml:"id"`
	Title        string   `yaml:"标题"`
	Difficulty   int      `yaml:"难度"`
	Tags         []string `yaml:"标签"`
	Description  string   `yaml:"问题描述"`
	InputFormat  string   `yaml:"输入形式"`
	SampleInput  string   `yaml:"样例输入"`
	SampleOutput string   `yaml:"样例输出"`
	SampleNote   string   `yaml:"样例说明"`
	ScoreStd     string   `yaml:"评分标准"`
	Hint         string   `yaml:"提示说明"`
}

// ------------------ 工具函数 ----------------------
func zipFolder(sourceDir, zipPath string) error {
	// 创建zip文件
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("创建zip文件失败：%v", err)
	}
	defer zipFile.Close()

	// 创建zip写入器
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// 遍历文件夹所有文件
	err = filepath.Walk(sourceDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录本身（只处理文件和子目录）
		if info.IsDir() {
			return nil
		}

		// 计算文件在zip中的相对路径（去除sourceDir前缀）
		relativePath, err := filepath.Rel(sourceDir, filePath)
		if err != nil {
			return fmt.Errorf("计算相对路径失败：%v", err)
		}

		// 在zip中创建文件入口
		zipEntry, err := zipWriter.Create(relativePath)
		if err != nil {
			return fmt.Errorf("创建zip入口失败：%v", err)
		}

		// 读取源文件并写入zip
		sourceFile, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("打开源文件失败：%v", err)
		}
		defer sourceFile.Close()

		_, err = io.Copy(zipEntry, sourceFile)
		return err
	})

	if err != nil {
		return fmt.Errorf("压缩过程失败：%v", err)
	}
	return nil
}

func httpPostJSON(url string, body any, target any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("json.Marshal 失败: %v", err)
	}

	req, err := http.NewRequest("POST", url, io.NopCloser(bytes.NewReader(b)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", Token)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode != 201 {
		return fmt.Errorf("状态码错误: %d, 响应内容: %s", resp.StatusCode, string(raw))
	}

	if target != nil {
		return json.Unmarshal(raw, target)
	}
	return nil
}

func downloadFile(url, path string) error {
	// 创建http客户端，添加超时
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("下载文件失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}

	// 创建目录
	os.MkdirAll(filepath.Dir(path), 0755)

	// 创建文件
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建文件失败: %v", err)
	}
	defer f.Close()

	// 写入文件
	_, err = io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}

	return nil
}

// ------------------ 静态资源相关函数 ----------------------

// 修复：移除反向引用，使用RE2支持的正则语法匹配img的src属性
func extractImageUrls(text string) []string {
	// 匹配规则：
	// 1. <img 开头，匹配任意非>字符
	// 2. src= （支持前后空格）
	// 3. 匹配src值：可以是 "" 包裹、'' 包裹、无包裹（直到遇到空格/>/换行）
	// 分三种情况匹配，避免反向引用
	re := regexp.MustCompile(`<img[^>]*\ssrc\s*=\s*"([^"]+)"[^>]*>|<img[^>]*\ssrc\s*=\s*'([^']+)'[^>]*>|<img[^>]*\ssrc\s*=\s*([^ >'"]+)[^>]*>`)
	matches := re.FindAllStringSubmatch(text, -1)

	urls := make([]string, 0, len(matches))
	for _, match := range matches {
		// 匹配结果中，第1/2/3组分别对应双引号/单引号/无引号的src值
		for i := 1; i <= 3; i++ {
			if match[i] != "" {
				urls = append(urls, match[i])
				break
			}
		}
	}

	return urls
}

// 生成唯一的文件名（避免不同URL但文件名相同的冲突）
func generateUniqueFilename(url string, index int) string {
	// 获取原始文件名
	filename := filepath.Base(url)
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)

	// 对URL进行简单哈希，生成唯一标识
	hash := 0
	for _, c := range url {
		hash = (hash*31 + int(c)) % 10000
	}

	// 生成唯一文件名：原名称_哈希值.后缀
	return fmt.Sprintf("%s_%04d%s", name, hash, ext)
}

// 下载单张图片到对应题号目录
func downloadImage(url, problemID string, index int) (string, error) {
	// 生成唯一文件名
	filename := generateUniqueFilename(url, index)
	savePath := filepath.Join(AssetsFolder, problemID, filename)

	if err := os.MkdirAll(filepath.Dir(savePath), 0755); err != nil {
		return "", fmt.Errorf("创建图片目录失败：%v", err)
	}

	fmt.Printf("    下载图片 → %s (来源: %s)\n", savePath, url)
	err := downloadFile(url, savePath)
	if err != nil {
		return "", fmt.Errorf("下载失败：%v", err)
	}

	return filename, nil
}

// 批量下载当前题目的所有静态图片，返回URL到本地路径的映射
func downloadAllImages(problem Problem) map[string]string {
	problemID := fmt.Sprintf("%d", problem.Meta.DisplayID)
	var allText strings.Builder

	// 收集所有contentSections中的文本
	contentSections, ok := problem.LocalizedContentsOfLocale["contentSections"].([]interface{})
	if ok {
		for _, sec := range contentSections {
			secMap, ok := sec.(map[string]interface{})
			if ok {
				text, _ := secMap["text"].(string)
				allText.WriteString(text)
			}
		}
	}

	// 提取所有图片URL
	urls := extractImageUrls(allText.String())
	if len(urls) == 0 {
		return nil
	}

	// 去重
	uniqueUrls := make(map[string]bool)
	for _, url := range urls {
		uniqueUrls[url] = true
	}

	fmt.Printf("  发现 %d 张图片，开始下载...\n", len(uniqueUrls))

	// 下载图片并记录URL到本地文件名的映射
	urlToLocalPath := make(map[string]string)
	index := 0
	for url := range uniqueUrls {
		filename, err := downloadImage(url, problemID, index)
		if err != nil {
			fmt.Printf("    × 图片下载失败（%s）：%v\n", url, err)
			index++
			continue
		}
		// 存储映射关系：原始URL → 本地相对路径
		urlToLocalPath[url] = fmt.Sprintf("./assets/%s/%s", problemID, filename)
		index++
	}

	return urlToLocalPath
}

// 修复：替换img标签为本地路径（使用无反向引用的正则）
func replaceImageTagsWithRelativePath(text string, urlToLocalPath map[string]string) string {
	if text == "" || urlToLocalPath == nil {
		return text
	}

	// 分三种情况匹配img标签（双引号/单引号/无引号），避免反向引用
	// 匹配后通过回调函数替换
	re := regexp.MustCompile(`(<img[^>]*\ssrc\s*=\s*)"([^"]+)"([^>]*>)|(<img[^>]*\ssrc\s*=\s*)'([^']+)'([^>]*>)|(<img[^>]*\ssrc\s*=\s*)([^ >'"]+)([^>]*>)`)

	result := re.ReplaceAllStringFunc(text, func(match string) string {
		submatches := re.FindStringSubmatch(match)
		var srcUrl string
		// 提取src值（第2/5/8组分别对应双引号/单引号/无引号的src）
		if submatches[2] != "" {
			srcUrl = submatches[2]
		} else if submatches[5] != "" {
			srcUrl = submatches[5]
		} else if submatches[8] != "" {
			srcUrl = submatches[8]
		} else {
			return match // 无src值，保留原标签
		}

		// 查找本地路径并替换
		if localPath, ok := urlToLocalPath[srcUrl]; ok {
			return localPath
		}
		return match // 未找到映射，保留原标签
	})

	return result
}

// ------------------ 下载所有类型的文件 ----------------------

func fetchFileList(problemId int, fileType string) ([]string, []string, error) {
	req := DownloadReq{
		ProblemId:    problemId,
		Type:         fileType,
		FilenameList: []string{},
	}

	var resp DownloadResp
	err := httpPostJSON(BaseAPI+"/problem/downloadProblemFiles", req, &resp)
	if err != nil {
		return nil, nil, err
	}

	names := []string{}
	urls := []string{}
	for _, f := range resp.DownloadInfo {
		names = append(names, f.Filename)
		urls = append(urls, f.DownloadUrl)
	}
	return names, urls, nil
}

func downloadAllFiles(pid int, displayId int) {
	group := fmt.Sprintf("P%02d", displayId)

	types := map[string]string{
		"TestData":       "testData",
		"AdditionalFile": "additionalFile",
	}

	for apiType, folder := range types {
		names, urls, err := fetchFileList(pid, apiType)
		if err != nil {
			fmt.Printf("  × 获取 %s 列表失败: %v\n", apiType, err)
			continue
		}

		for i := range names {
			savePath := filepath.Join(OutputFolder, folder, group, names[i])
			fmt.Printf("    下载 %s → %s\n", names[i], savePath)
			if err := downloadFile(urls[i], savePath); err != nil {
				fmt.Printf("    × 下载失败: %v\n", err)
			}
		}
	}
}

// ------------------ 数据转换工具函数 ----------------------
func getStringFromInterface(val interface{}) string {
	if val == nil {
		return ""
	}
	str, _ := val.(string)
	return str
}

func extractTags(tagsInterface interface{}) []string {
	tags, ok := tagsInterface.([]interface{})
	if !ok {
		fmt.Println("警告：标签数据格式错误，不是切片")
		return []string{}
	}
	var res []string
	for _, tag := range tags {
		tagMap, ok := tag.(map[string]interface{})
		if !ok {
			continue
		}
		name, ok := tagMap["name"].(string)
		if ok {
			res = append(res, name)
		}
	}
	return res
}

func extractContentSection(sectionsInterface interface{}, targetTitles ...string) string {
	sections, ok := sectionsInterface.([]interface{})
	if !ok {
		fmt.Printf("警告：内容区块不是切片（目标标题：%v）\n", targetTitles)
		return ""
	}
	for _, sec := range sections {
		secMap, ok := sec.(map[string]interface{})
		if !ok {
			continue
		}
		secTitle, ok := secMap["sectionTitle"].(string)
		if !ok {
			continue
		}
		for _, target := range targetTitles {
			if secTitle == target {
				text, _ := secMap["text"].(string)
				return text
			}
		}
	}
	fmt.Printf("警告：未找到目标标题 %v\n", targetTitles)
	return ""
}

func extractSampleInput(samplesInterface interface{}) string {
	samples, ok := samplesInterface.([]interface{})
	if !ok || len(samples) == 0 {
		fmt.Println("警告：样例数据为空或格式错误")
		return ""
	}
	firstSample, ok := samples[0].(map[string]interface{})
	if !ok {
		fmt.Println("警告：样例元素不是 map")
		return ""
	}
	input, _ := firstSample["inputData"].(string)
	return input
}

func extractSampleOutput(samplesInterface interface{}) string {
	samples, ok := samplesInterface.([]interface{})
	if !ok || len(samples) == 0 {
		fmt.Println("警告：样例数据为空或格式错误")
		return ""
	}
	firstSample, ok := samples[0].(map[string]interface{})
	if !ok {
		fmt.Println("警告：样例元素不是 map")
		return ""
	}
	output, _ := firstSample["outputData"].(string)
	return output
}

// 格式化多行文本，统一换行符并去除首尾空白
func formatMultiLine(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	return text
}

// 生成YAML时直接拼接字符串（避免正则替换的混乱）
func buildYAML(target TargetYAML) string {
	var buf bytes.Buffer
	buf.WriteString(`version: "1.0"`)
	buf.WriteString("\nproblems:\n")

	for _, problem := range target.Problems {
		buf.WriteString("  - id: " + problem.ID + "\n")
		buf.WriteString("    标题: " + problem.Title + "\n")
		buf.WriteString("    难度: " + fmt.Sprintf("%d", problem.Difficulty) + "\n")
		buf.WriteString("    标签:\n")
		for _, tag := range problem.Tags {
			buf.WriteString("      - " + tag + "\n")
		}

		// 处理需要 |- 格式的字段
		writeLiteralField(&buf, "    问题描述", problem.Description)
		writeLiteralField(&buf, "    输入形式", problem.InputFormat)
		writeLiteralField(&buf, "    样例输入", problem.SampleInput)
		writeLiteralField(&buf, "    样例输出", problem.SampleOutput)
		writeLiteralField(&buf, "    样例说明", problem.SampleNote)
		writeLiteralField(&buf, "    评分标准", problem.ScoreStd)
		writeLiteralField(&buf, "    提示说明", problem.Hint)
	}

	return buf.String()
}

// 写入单个需要 |- 格式的字段
func writeLiteralField(buf *bytes.Buffer, fieldName, content string) {
	buf.WriteString(fieldName + ": |-\n")
	if content == "" {
		buf.WriteString("      \n")
		return
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		buf.WriteString("      " + line + "\n")
	}
}

// ------------------ 主流程 ----------------------

func main() {
	fmt.Printf("开始抓取题目： %d 到 %d\n\n", StartID, EndID)

	// 创建输出目录
	os.MkdirAll(OutputFolder, 0755)
	os.MkdirAll(AssetsFolder, 0755)

	var allProblems []Problem
	// 存储每个题目的图片URL映射
	problemImageMaps := make(map[int]map[string]string)

	for id := StartID; id <= EndID; id++ {
		fmt.Printf("\n--- 正在处理 displayId=%d ---\n", id)

		req := GetProblemRequest{
			DisplayId:               id,
			LocalizedContentsLocale: "zh_CN",
			JudgeInfo:               true,
			JudgePre:                true,
			Samples:                 true,
			TagsLocale:              "zh_CN",
		}

		var p Problem
		err := httpPostJSON(BaseAPI+"/problem/getProblem", req, &p)
		if err != nil {
			fmt.Printf("获取题目 %d 失败: %v\n", id, err)
			continue
		}

		// 下载当前题目的所有静态图片，并保存URL映射
		urlToLocalPath := downloadAllImages(p)
		problemImageMaps[p.Meta.DisplayID] = urlToLocalPath

		var title string
		titleVal, ok := p.LocalizedContentsOfLocale["title"]
		if ok {
			title, _ = titleVal.(string)
		}
		fmt.Printf("解析到标题：%s\n", title)
		fmt.Printf("解析到标签数量：%d\n", len(extractTags(p.TagsOfLocale)))

		allProblems = append(allProblems, p)
		downloadAllFiles(p.Meta.ID, p.Meta.DisplayID)
		fmt.Printf("处理完成 %d\n", id)
	}

	// 转换为目标数据结构
	target := TargetYAML{Version: "1.0"}
	for _, problem := range allProblems {
		contentSections := problem.LocalizedContentsOfLocale["contentSections"]
		actualProblemID := fmt.Sprintf("%d", problem.Meta.DisplayID)
		// 获取当前题目的图片URL映射
		urlToLocalPath := problemImageMaps[problem.Meta.DisplayID]

		// 提取原始内容并替换图片标签为本地相对路径
		description := formatMultiLine(extractContentSection(contentSections, "题目描述"))
		description = replaceImageTagsWithRelativePath(description, urlToLocalPath)

		sampleNote := formatMultiLine(extractContentSection(contentSections, "样例", "样例 1"))
		sampleNote = replaceImageTagsWithRelativePath(sampleNote, urlToLocalPath)

		hint := formatMultiLine(extractContentSection(contentSections, "数据范围与提示", "提示说明"))
		hint = replaceImageTagsWithRelativePath(hint, urlToLocalPath)

		targetProblem := TargetProblem{
			ID:           actualProblemID,
			Title:        getStringFromInterface(problem.LocalizedContentsOfLocale["title"]),
			Difficulty:   2,
			Tags:         extractTags(problem.TagsOfLocale),
			Description:  description,
			InputFormat:  formatMultiLine(extractContentSection(contentSections, "输入格式")),
			SampleInput:  formatMultiLine(extractSampleInput(problem.Samples)),
			SampleOutput: formatMultiLine(extractSampleOutput(problem.Samples)),
			SampleNote:   sampleNote,
			ScoreStd:     formatMultiLine(extractContentSection(contentSections, "评分标准")),
			Hint:         hint,
		}
		target.Problems = append(target.Problems, targetProblem)
	}

	// 直接生成YAML字符串
	finalYAMLStr := buildYAML(target)

	// 保存YAML文件
	if err := os.WriteFile(YAMLPath, []byte(finalYAMLStr), 0644); err != nil {
		fmt.Printf("保存YAML失败: %v\n", err)
		return
	}

	// 可选：压缩文件夹
	zipPath := OutputFolder + `.zip`
	fmt.Printf("\n开始压缩文件夹 %s → %s\n", OutputFolder, zipPath)
	if err := zipFolder(OutputFolder, zipPath); err != nil {
		fmt.Printf("压缩失败：%v\n", err)
	} else {
		fmt.Printf("压缩成功！压缩包路径：%s\n", zipPath)
	}

	fmt.Println("\n全部完成。YAML路径：", YAMLPath)
}
