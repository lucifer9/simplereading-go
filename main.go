package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/go-shiori/go-readability"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var (
	version = "0.9.1"
	build   = "Custom"
)

var (
	BOOKSITE string
	FONTSIZE int
	UA       string
	HOST     string
	PORT     string
	SCHEME   string
)

func defaultHandler(w http.ResponseWriter, req *http.Request) {
	rpURL, _ := url.Parse(BOOKSITE)
	rp := httputil.NewSingleHostReverseProxy(rpURL)
	rp.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Fetcher error:%v\n", err)
		// http.Error(w, err.Error(), http.StatusInternalServerError)
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Refresh", "3")
		_, _ = w.Write([]byte("<html><head><meta http-equiv=\"refresh\" content=\"3\"/></head><body>Error: " + err.Error() + "</body></html>"))
	}

	rp.Director = func(r *http.Request) {
		r.Host = "m.booklink.me"
		r.URL.Host = r.Host
		r.URL.Scheme = "https"
		r.Header.Set("Accept-Encoding", "gzip")
		log.Println(r.URL.String())
	}
	rp.ModifyResponse = func(r *http.Response) error {
		if cookie := r.Header.Get("Set-Cookie"); cookie != "" {
			cookie = strings.ReplaceAll(cookie, ".booklink.me", HOST)
			r.Header.Set("Set-Cookie", cookie)
		}
		if loc := r.Header.Get("Location"); loc != "" {
			if i := strings.Index(loc, HOST); i == -1 {
				var sb strings.Builder
				sb.WriteString(SCHEME)
				sb.WriteString("://")
				sb.WriteString(HOST)
				if PORT != "" {
					sb.WriteString(":")
					sb.WriteString(PORT)
				}
				sb.WriteString("/?dest=")
				sb.WriteString(loc)
				//r.Header.Set("Location", SCHEME+HOST+":"+PORT+"/?dest="+loc)
				r.Header.Set("Location", sb.String())
			}
		}

		b, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		err = r.Body.Close()
		if err != nil {
			return err
		}
		if c := r.Header.Get("Content-Encoding"); c == "gzip" {
			gr, _ := gzip.NewReader(bytes.NewBuffer(b))
			b, err = io.ReadAll(gr)
			_ = gr.Close()
			if err != nil {
				return err
			}
		}
		b = bytes.ReplaceAll(b, []byte("adsbygoogle"), []byte("xxxxxxx"))
		// b = bytes.ReplaceAll(b, []byte("<body>"), []byte("<body><style>a:hover{color:#fff}a{color:#fff;}body{background-color: black;color: #fff;}</style>"))
		b = bytes.ReplaceAll(b, []byte("<li class=\"hla\">"), []byte("<li class=\"\">"))
		// b = bytes.ReplaceAll(b, []byte("<font color=\"#646464\""), []byte("<font color=\"#aaa\""))
		if index := bytes.Index(b, []byte("slist sec")); index != -1 {
			b = bytes.ReplaceAll(b, []byte("<body>"), []byte("<style>ul.list.sec {display: none;}</style>"))
		}
		b = bytes.ReplaceAll(b, []byte("www.google.com/search?ie=utf-8&"), []byte("duckduckgo.com/?ia=qa&"))
		newb := new(bytes.Buffer)
		gw := brotli.NewWriter(newb)
		_, _ = gw.Write(b)
		_ = gw.Close()
		b = newb.Bytes()
		body := io.NopCloser(bytes.NewReader(b))
		r.Body = body
		r.ContentLength = int64(len(b))
		r.Header.Set("Content-Encoding", "br")
		r.Header.Set("Content-Length", strconv.Itoa(len(b)))
		return nil
	}

	qs := req.URL.Query()
	dest := qs.Get("dest")
	//listen := qs.Get("listen")
	//if dest == "" && listen == "" {
	if dest == "" {
		rp.ServeHTTP(w, req)
	} else {
		fmt.Println(dest)
		if strings.Contains(dest, "fkzww.net") {
			http.Redirect(w, req, dest, 302)
		} else {
			var err error
			var base *url.URL
			var article *readability.Article
			re, _ := regexp.Compile("xxx")
			base, err = url.Parse(dest)
			if err != nil {
				error500(w, err)
				return
			}
			if strings.Contains(dest, "/") && strings.Contains(dest, ".") {
				first := strings.LastIndex(dest, "/") + 1
				last := strings.LastIndex(dest, ".")
				if first < last {
					base := dest[first:last]
					re, err = regexp.Compile(fmt.Sprintf("%s_\\d+", base))
				}
			}
			if err != nil {
				error500(w, err)
				return
			}
			article, err = getContent(base, re)
			if err != nil {
				error500(w, err)
				return
			}
			next := article.TextContent
			for next != "" {
				nextUrl, _ := url.Parse(next)
				nextUrl = base.ResolveReference(nextUrl)
				a1, err := getContent(nextUrl, re)
				if err != nil {
					error500(w, err)
					return
				}
				article.Content += a1.Content
				next = a1.TextContent
			}
			title := article.Title
			content := article.Content
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "text/html;charset=UTF-8")
			// toWrite := `<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0" /><title>` +
			//	title + `</title></head><body><h3>` + title + `</h3><style>body {background-color: black;font-size:` + strconv.Itoa(FONTSIZE) +
			//	";color:#fff;}</style>\n" + content + `</body></html>`
			sb := strings.Builder{}
			sb.WriteString("<html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\" /><title>")
			sb.WriteString(title)
			sb.WriteString("</title></head><body><h3>")
			sb.WriteString(title)
			sb.WriteString("</h3><style>body {font-size:")
			sb.WriteString(strconv.Itoa(FONTSIZE))
			sb.WriteString(";}</style>\n")
			sb.WriteString(content)
			sb.WriteString("</body></html>")
			//toWrite := `<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0" /><title>` +
			//	title + `</title></head><body><h3>` + title + `</h3><style> p{text-indent:2em; font-size:` + strconv.Itoa(FONTSIZE) +
			//	";}</style>\n" + content + `</body></html>`
			_, _ = w.Write([]byte(sb.String()))
		}
	}
	//if listen != "" {
	//	fmt.Println(listen)
	//	listen = strings.TrimPrefix(listen, SCHEME+HOST+"/?dest=")
	//	out := MP3CACHE[listen]
	//	if out == "" {
	//		out = time.Now().Format("20060102150405") + ".mp3"
	//
	//		outFile := filepath.Join(WEBROOT, out)
	//		article, err := getContent(listen)
	//		if err != nil {
	//			error500(w, err)
	//			return
	//		}
	//		content := article.TextContent
	//		if err := getMP3(content, outFile); err != nil {
	//			error500(w, err)
	//			return
	//		}
	//		MP3CACHE[listen] = out
	//	}
	//	w.WriteHeader(http.StatusOK)
	//	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	//	var sb strings.Builder
	//	sb.WriteString(SCHEME)
	//	sb.WriteString(HOST)
	//	if PORT != "" {
	//		sb.WriteString(":")
	//		sb.WriteString(PORT)
	//	}
	//	sb.WriteString(out)
	//	//r.Header.Set("Location", SCHEME+HOST+":"+PORT+"/?dest="+loc)
	//	mp3 := sb.String()
	//	//mp3 := SCHEME + HOST + ":" + PORT + `/` + out
	//	toWrite := `<!doctype html><html><body><audio controls height="270" width="480"><source src="` + mp3 + `"></audio></body></html>`
	//	_, _ = w.Write([]byte(toWrite))
	//}
}

func error500(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(err.Error()))
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("Simplreading " + version + " (Happy Reading!) " +
			build + " (" + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + ")")
		return
	}
	DEBUG := os.Getenv("DEBUG")
	BOOKSITE = "https://m.booklink.me/"
	FONTSIZE = 17
	//WEBROOT = "/tmp"
	//TtsBase = "http://tsn.baidu.com/text2audio"
	//TtsSegLen = 60
	//TtsPer = 5118
	//TtsSpd = 10
	//TtsVol = 8
	UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.102 Safari/537.36"
	HOST = os.Getenv("HOST")
	if HOST == "" {
		HOST = "127.0.0.1"
	}
	SCHEME = os.Getenv("SCHEME")
	if SCHEME == "" {
		SCHEME = "http"
	}
	//MP3CACHE = make(map[string]string)
	http.HandleFunc("/", defaultHandler)
	localPort := os.Getenv("LOCAL_PORT")
	if localPort == "" {
		localPort = "9005"
	}
	PORT = os.Getenv("PORT")
	if DEBUG != "" {
		PORT = localPort
	}
	listenLocal := os.Getenv("LISTEN_LOCAL")
	var listenAddr string
	if listenLocal != "" {
		listenAddr = "127.0.0.1"
	} else {
		listenAddr = "0.0.0.0"
	}
	_ = http.ListenAndServe(listenAddr+":"+localPort, nil)
}

//func getMP3(content, out string) error {
//	runes := []rune(content)
//	length := len(runes)
//	total := length
//	var chans []chan bool
//	mp3s := make(map[int][]byte)
//	i := 0
//	for length >= 0 {
//		c := make(chan bool)
//		chans = append(chans, c)
//		go func(index int, success chan bool) {
//			b := index * TtsSegLen
//			e := (index + 1) * TtsSegLen
//			if e > total {
//				e = total
//			}
//			seq := url.QueryEscape(string(runes[b:e]))
//			data := url.Values{}
//			// data={'lan':'zh','ie':'UTF-8','spd':10,'tex':urllib.parse.quote(words), 'per': 5118, 'cuid':'baidu_speech_demo','idx':1,'cod':2,'ctp':1,'pdt':1,'vol':8,'pit':5,'_res_tag_':'audio'}
//			data.Set("lan", "zh")
//			data.Set("spd", strconv.Itoa(TtsSpd))
//			data.Set("tex", seq)
//			data.Set("per", strconv.Itoa(TtsPer))
//			data.Set("idx", "1")
//			data.Set("cuid", "baidu_speech_demo")
//			data.Set("ctp", "1")
//			data.Set("cod", "2")
//			data.Set("vol", strconv.Itoa(TtsVol))
//			data.Set("pit", "5")
//			data.Set("pdt", "220")
//			data.Set("_res_tag_", "audio")
//			client := &http.Client{}
//			req, err := http.NewRequest(http.MethodPost, TtsBase, strings.NewReader(data.Encode())) //nolint
//			if err != nil {
//				log.Printf("Error creating tts req %v\n", req)
//				success <- false
//				return
//			}
//			resp, err := client.Do(req)
//
//			if err != nil || (resp != nil && resp.Header.Get("Content-Type") != "audio/mp3") {
//				buf, _ := io.ReadAll(resp.Body)
//				log.Printf("Error get tts resp %v\n", string(buf))
//				success <- false
//				return
//			}
//			defer func() {
//				_ = resp.Body.Close()
//			}()
//
//			buf, err := io.ReadAll(resp.Body)
//			log.Printf("in thread %d, get buf length %d\n", index, len(buf))
//			if err != nil {
//				log.Printf("Error reading tts resp %v\n", resp)
//				success <- false
//				return
//			}
//			mp3s[index] = buf
//			success <- true
//		}(i, c)
//		i++
//		length -= TtsSegLen
//	}
//	for _, ch := range chans {
//		if !<-ch {
//			return errors.New("error getting mp3")
//		}
//	}
//
//	err := mergeMP3(mp3s, out)
//	if err != nil {
//		return err
//	}
//	return nil
//}

func getContent(srcPath *url.URL, re *regexp.Regexp) (*readability.Article, error) {
	article, buf := getOneArticle(srcPath)
	if article == nil {
		return nil, errors.New("null article")
	}
	var nextLink string
	doc, err := html.Parse(bytes.NewReader(buf))
	if err != nil {
		nextLink = ""
	}
	nextLink = getNextLink(doc, re)
	//if nextLink == "" {
	//	return article, nil
	//}
	//r, _ := regexp.Compile(srcPath[strings.LastIndex(srcPath, "/")+1:strings.LastIndex(srcPath, ".")] + `_\d+`)
	////nolint // lastPart := nextLink[strings.LastIndex(nextLink, "/")+1 : strings.LastIndex(nextLink, ".")]
	//for nextLink != "" && !strings.HasSuffix(nextLink, "/") &&
	//	(len(nextLink[strings.LastIndex(nextLink, "/")+1:strings.LastIndex(nextLink, ".")]) == 1 ||
	//		r.MatchString(nextLink[strings.LastIndex(nextLink, "/")+1:strings.LastIndex(nextLink, ".")])) {
	//	l, _ := url.Parse(srcPath)
	//	nl, _ := url.Parse(nextLink)
	//	nl = l.ResolveReference(nl)
	//	article1, buf1 := getOneArticle(nl.String())
	//	if article1 == nil {
	//		break
	//	}
	//	article.Content += article1.Content
	//	article.TextContent += article1.TextContent
	//	nextLink = getNextLink(buf1)
	//	// lastPart = nextLink[strings.LastIndex(nextLink, "/")+1 : strings.LastIndex(nextLink, ".")]
	//}
	article.TextContent = nextLink
	return article, nil
}

func determineEncoding(r *bufio.Reader, contentType string) encoding.Encoding {
	nBytes, err := r.Peek(1024)
	if err != nil {
		log.Printf("Fetcher error:%v\n", err)
		return unicode.UTF8
	}
	e, _, _ := charset.DetermineEncoding(nBytes, contentType)
	return e
}

// Open or fetch web page that will be parsed
func getOneArticle(destUrl *url.URL) (*readability.Article, []byte) {
	var (
		pageURL   string
		srcReader io.Reader
	)
	dest := destUrl.String()
	log.Printf("Getting page: %s\n", dest)
	req, err := http.NewRequest(http.MethodGet, dest, nil)
	if err != nil {
		log.Printf("Error creating req to %s. %v", dest, err)
		return nil, nil
	}
	req.Header.Set("Referer", dest)
	req.Header.Set("User-Agent", UA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Printf("Error getting resp from %s. %v\n", dest, err)
		return nil, nil
	}
	bodyReader := bufio.NewReader(resp.Body)
	e := determineEncoding(bodyReader, resp.Header.Get("Content-Type"))
	utf8Reader := transform.NewReader(bodyReader, e.NewDecoder())
	srcReader = utf8Reader
	pageURL = dest
	defer func() {
		_ = resp.Body.Close()
	}()
	buf := bytes.NewBuffer(nil)
	tee := io.TeeReader(srcReader, buf)
	parsedURL, _ := url.Parse(pageURL)
	article, err := readability.FromReader(tee, parsedURL)
	if err != nil {
		return nil, nil
	}
	return &article, buf.Bytes()
}

func getNextLink(node *html.Node, re *regexp.Regexp) string {
	if node.Type == html.ElementNode && node.Data == "a" {
		for _, a := range node.Attr {
			if a.Key == "href" {
				url1 := a.Val
				if len(url1) >= 4 && strings.Contains(url1, "/") && strings.Contains(url1, ".") {
					first := strings.LastIndex(url1, "/") + 1
					last := strings.LastIndex(url1, ".")
					if first < last {
						dest := url1[first:last]
						if re.MatchString(dest) {
							return url1
						}
					}
				}
			}
		}
	}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		if link := getNextLink(c, re); link != "" {
			return link
		}
	}
	return ""
	//doc, err := html.Parse(bytes.NewReader(buf))
	//if err != nil {
	//	return ""
	//}
	//nextURLs := make(map[string]string)
	//var f func(*html.Node, map[string]string)
	//f = func(n *html.Node, m map[string]string) {
	//	if n.Type == html.ElementNode && n.Data == "a" {
	//		if n.FirstChild != nil && (strings.TrimSpace(n.FirstChild.Data) == "下一页" || strings.TrimSpace(n.FirstChild.Data) == "下一章") {
	//			for _, a := range n.Attr {
	//				if a.Key == "href" {
	//					m[n.FirstChild.Data] = strings.TrimSpace(a.Val)
	//				}
	//			}
	//		}
	//	}
	//	for c := n.FirstChild; c != nil; c = c.NextSibling {
	//		f(c, m)
	//	}
	//}
	//f(doc, nextURLs)
	//nextLink := nextURLs["下一章"]
	//if nextURLs["下一页"] != "" {
	//	nextLink = nextURLs["下一页"]
	//}
	//return nextLink
}

//func mergeMP3(infiles map[int][]byte, out string) error {
//	outfile, err := os.Create(filepath.FromSlash(out))
//
//	if err != nil {
//		return err
//	}
//	for i := 0; i < len(infiles); i++ {
//		_, err := outfile.Write(infiles[i])
//		if err != nil {
//			return err
//		}
//	}
//
//	return outfile.Close()
//}
