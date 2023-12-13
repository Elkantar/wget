package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

func main() {
	// different falg a utilisé
	backgroundFlag := flag.Bool("B", false, "Download in background")               // télécharge en arriere plan
	outputFlag := flag.String("O", "", "Save file with a different name")           // change le nom du fichier telechargé
	pathFlag := flag.String("P", "", "Specify download path")                       // permet de specifié le chemin de téléchargement.
	rateLimitFlag := flag.String("rate-limit", "", "Set download speed limit")      // change la vitesse de téléchargement
	fileListFlag := flag.String("i", "", "File containing multiple download links") // permet de telechargé plusieurs fichiers  en meme temps

	mirrorFlag := flag.Bool("mirror", false, "Mirror a website")
	rejectFlag := flag.String("R", "", "Exclude file suffixes")
	excludeFlag := flag.String("X", "", "Exclude paths")

	// permet de recupéré tout les flag
	flag.Parse()

	args := os.Args
	lenargs := len(args)
	urlFlag := args[lenargs-1]

	if *mirrorFlag {
		fmt.Println("mirror")
		downloadPath := *pathFlag
		if downloadPath == "" {
			downloadPath = ""
		}
		mirrorWebsite(urlFlag, downloadPath, *rejectFlag, *excludeFlag)
	}

	if *fileListFlag != "" {
		downloadFromList(*fileListFlag)
		return
	}
	if *backgroundFlag {
		fmt.Println("Output will be written to \"wget-log\".")
		go func() {
			logFile, err := os.Create("wget-log")
			if err != nil {
				fmt.Println("logFile")
				fmt.Printf("Error creating log file: %s\n", err)
				return
			}
			defer logFile.Close()

			os.Stdout = logFile
			if urlFlag != "" {
				fmt.Println("ici downloadURL")
				downloadURL(urlFlag, *outputFlag, *pathFlag, *rateLimitFlag)
			}
		}()
	}
	if urlFlag != "" && !*mirrorFlag {
		downloadURL(urlFlag, *outputFlag, *pathFlag, *rateLimitFlag)
	}
}

func downloadURL(urlStr, output, downloadPath, rateLimit string) {
	fmt.Printf("start at %s\n", getCurrentTimestamp())

	response, err := http.Get(urlStr)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusForbidden {
		fmt.Println("403 Forbidden Error: You don't have permission to access this resource.")
		return
	} else if response.StatusCode != http.StatusOK {
		fmt.Printf("sending request, awaiting response... status %s\n", response.Status)
		return
	}
	var result string
	totalSize, _ := strconv.Atoi(response.Header.Get("Content-Length"))
	filename := path.Base(urlStr)
	i := 0
	if output != "" {
		for _, ExtensionFile := range filename {
			if ExtensionFile == '.' || i == 1 {
				i = 1
				result += string(ExtensionFile)
			}
		}
	}
	if output != "" {
		filename = output
	}

	fmt.Println(downloadPath)
	var filePath string
	if downloadPath != "" {
		filePath = path.Join(downloadPath, filename)
	} else {
		filePath = filename + result
	}

	fmt.Printf("content size: %d [~%.2fMB]\n", totalSize, float64(totalSize)/(1024*1024))
	fmt.Printf("saving file to: %s\n", filePath)

	file, err := os.Create(filePath)
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		return
	}
	defer file.Close()

	var reader io.Reader = response.Body
	if rateLimit != "" {
		limit, err := parseRateLimit(rateLimit)
		if err != nil {
			fmt.Printf("Error parsing rate limit: %s\n", err)
			return
		}
		reader = newRateLimitedReader(reader, limit)
	}

	downloadedSize := 0
	buffer := make([]byte, 1024)
	startTime := time.Now()

	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			downloadedSize += n
			file.Write(buffer[:n])

			elapsed := time.Since(startTime).Seconds()
			speed := float64(downloadedSize) / (1024 * elapsed)

			fmt.Printf("\r%.2f KiB / %.2f KiB [%s] %.2f%% %.2f KiB/s %ds",
				float64(downloadedSize)/1024,
				float64(totalSize)/1024,
				strings.Repeat("=", int(float64(downloadedSize)/float64(totalSize)*50)),
				(float64(downloadedSize)/float64(totalSize))*100,
				speed,
				int(float64(totalSize-downloadedSize)/speed),
			)

			if downloadedSize >= totalSize {
				break
			}
		}

		if err != nil {
			if err != io.EOF {
				fmt.Printf("\nError: %s\n", err)
			}
			break
		}
	}

	fmt.Printf("\nDownloaded [%s]\n", urlStr)
	fmt.Printf("finished at %s\n", getCurrentTimestamp())
}

func downloadFromList(fileListPath string) {
	file, err := os.Open(fileListPath)
	if err != nil {
		fmt.Printf("Error opening file list: %s\n", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var wg sync.WaitGroup

	for scanner.Scan() {
		urlStr := scanner.Text()
		wg.Add(1)
		go func() {
			defer wg.Done()
			downloadURL(urlStr, "", "", "")
		}()
	}

	wg.Wait()
	fmt.Printf("Download finished for all URLs in %s\n", fileListPath)
}

func getCurrentTimestamp() string {
	now := time.Now()
	return now.Format("2006-01-02 15:04:05")
}

func parseRateLimit(rateLimit string) (int64, error) {
	valueStr := strings.TrimRightFunc(rateLimit, func(r rune) bool {
		return !unicode.IsDigit(r)
	})
	unit := strings.ToLower(strings.TrimLeftFunc(rateLimit, unicode.IsDigit))

	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		fmt.Println(err)
		return 0, err
	}

	switch unit {
	case "k":
		value *= 1024
	case "m":
		value *= 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid rate limit unit: %s", unit)
	}

	return value, nil
}

func newRateLimitedReader(reader io.Reader, limit int64) io.Reader {
	return &rateLimitedReader{reader: reader, limit: limit}
}

type rateLimitedReader struct {
	reader io.Reader
	limit  int64
}

func (rlr *rateLimitedReader) Read(p []byte) (n int, err error) {
	startTime := time.Now()
	n, err = rlr.reader.Read(p)
	elapsedTime := time.Since(startTime)

	// Calculate time needed to sleep to maintain the rate limit
	sleepTime := time.Duration(float64(n)/float64(rlr.limit)*float64(time.Second)) - elapsedTime
	if sleepTime > 0 {
		time.Sleep(sleepTime)
	}

	return n, err
}

func mirrorWebsite(urlStr, downloadPath, reject, exclude string) {
	fmt.Printf("Mirroring website: %s\n", urlStr)

	// Get the base URL
	baseURL, err := url.Parse(urlStr)
	if err != nil {
		fmt.Printf("Error parsing URL: %s\n", err)
		return
	}

	// Create a queue for URLs to be processed
	var queue []string
	queue = append(queue, urlStr)

	for len(queue) > 0 {
		currentURL := queue[0]
		queue = queue[1:]

		absURL, err := baseURL.Parse(currentURL)
		if err != nil {
			fmt.Printf("Error constructing absolute URL: %s\n", err)
			continue
		}

		pageContent, err := downloadPage(absURL.String())
		if err != nil {
			fmt.Printf("Error downloading page: %s\n", err)
			continue
		}

		pagePath := path.Join(downloadPath, "index.html", absURL.Path)
		err = savePage(pagePath, pageContent)
		if err != nil {
			err = nil
		}
		// Parse links in the page
		links := parseLinks(pageContent)
		for _, link := range links {
			// Enqueue internal links
			if strings.HasPrefix(link, baseURL.String()) && !strings.Contains(link, reject) {
				if exclude == "" || !strings.Contains(link, exclude) {
					queue = append(queue, link)
				}
			}
		}

		// Extract and save CSS content from stylesheet link
		if strings.HasSuffix(absURL.Path, ".css") {
			cssPath := path.Join(downloadPath, "css", "style.css")
			err = saveCSS(cssPath, pageContent)
			if err != nil {
				fmt.Printf("Error saving CSS: %s\n", err)
				continue
			}
		}

		// Parse HTML content to extract stylesheet links
		stylesheetLinks := extractStylesheetLinks(pageContent)
		for _, link := range stylesheetLinks {
			absLink, err := baseURL.Parse(link)
			if err != nil {
				fmt.Printf("Error parsing stylesheet link: %s\n", err)
				continue
			}
			queue = append(queue, absLink.String())
		}
		fmt.Println(exclude)
		if exclude != "/img" {
			// Parse HTML content to extract image links
			imageLinks, _ := extractImagePaths(pageContent)
			for _, link := range imageLinks {
				absLink, err := baseURL.Parse(link)
				if err != nil {
					fmt.Printf("Erreur lors de l'analyse du lien de l'image : %s\n", err)
				}

				// Télécharger et enregistrer l'image
				err = downloadAndSaveImage(absLink.String(), downloadPath)
				fmt.Println(absLink)
				fmt.Println(downloadPath)
				if err != nil {
					fmt.Printf("Erreur lors du téléchargement et de l'enregistrement de l'image : %s\n", err)
					continue
				}
			}
		}
	}

	fmt.Printf("Website mirroring finished\n")
}

func extractStylesheetLinks(htmlContent string) []string {
	doc, _ := html.Parse(strings.NewReader(htmlContent))
	var links []string
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			for _, attr := range n.Attr {
				if attr.Key == "rel" && attr.Val == "stylesheet" {
					for _, attr := range n.Attr {
						if attr.Key == "href" {
							links = append(links, attr.Val)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)
	return links
}

func downloadPage(urlStr string) (string, error) {
	response, err := http.Get(urlStr)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func savePage(filePath, content string) error {
	err := os.MkdirAll(path.Dir(filePath), os.ModePerm)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filePath, []byte(content), os.ModePerm)
	if err != nil {
		return err
	}

	return nil
}

func parseLinks(pageContent string) []string {
	re := regexp.MustCompile(`<link\s+href=["'](https?://[^"']+)["']`)
	matches := re.FindAllStringSubmatch(pageContent, -1)

	var links []string
	for _, match := range matches {
		links = append(links, match[1])
	}

	return links
}

func saveCSS(filePath, content string) error {
	i := 0
	result := ""
	for _, ch := range filePath {
		if ch != '/' && i < 3 {
			result += string(ch)
		} else {
			i++
			if i < 3 {
				result += string(ch)
			}
		}
	}
	err := os.MkdirAll(path.Dir(filePath), os.ModePerm)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filePath, []byte(content), os.ModePerm)
	if err != nil {
		return err
	}

	return nil
}

func extractImagePaths(htmlContent string) ([]string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, err
	}

	var imgPaths []string
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			for _, attr := range n.Attr {
				if attr.Key == "src" {
					imgPaths = append(imgPaths, attr.Val)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return imgPaths, nil
}

func downloadAndSaveImage(imgURL, imgFilePath string) error {
	var result string
	response, err := http.Get(imgURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	imgData, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	if imgFilePath == "" {
		imgFilePath = imgURL

		imgsplit := strings.Split(imgFilePath, "/")

		i := 0
		ver := 0
		for _, ch := range imgsplit {
			if ch != "img" && ver == 0 {
				i++
			} else {
				ver = 1
			}
		}
		fmt.Println(i)

		for j := 0; j < len(imgsplit); j++ {
			if j > i {
				result += "/" + imgsplit[j]
			}

			if j == i {
				result += "./" + imgsplit[j]
			}
		}
	}
	fmt.Println(result)
	err = os.MkdirAll(path.Dir(result), os.ModePerm)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(result, []byte(imgData), os.ModePerm)
	if err != nil {
		fmt.Println(err)
		return err
	}

	return nil
}
