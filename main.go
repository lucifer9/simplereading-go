package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var (
	BOOKSITE    string
	FONTSIZE    int
	WEBROOT     string
	TTS_BASE    string
	TTS_SEG_LEN int
	TTS_PER     int
	TTS_SPD     int
	TTS_VOL     int
	UA          string
	HOST        string
	PORT        int
	SCHEME      string
	MP3CACHE    map[string]string
)

func defaultHandler(w http.ResponseWriter, req *http.Request) {
	rpURL, _ := url.Parse(BOOKSITE)
	rp := httputil.NewSingleHostReverseProxy(rpURL)
	rp.Director = func(r *http.Request) {
		r.Host = "m.booklink.me"
		r.URL.Host = r.Host
		r.URL.Scheme = "https"
		r.Header.Del("Accept-Encoding")
		// log.Printf("\n%s: \t%+v\n\n", "req", r)
	}
	rp.ModifyResponse = func(r *http.Response) error {

		if cookie := r.Header.Get("Set-Cookie"); cookie != "" {
			cookie = strings.ReplaceAll(cookie, ".booklink.me", HOST)
			r.Header.Set("Set-Cookie", cookie)
		}
		if loc := r.Header.Get("Location"); loc != "" {
			if i := strings.Index(loc, HOST); i == -1 {
				r.Header.Set("Location", SCHEME+HOST+":"+strconv.Itoa(PORT)+"/?dest="+loc)
			}
		}

		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return err
		}
		err = r.Body.Close()
		if err != nil {
			return err
		}
		// if c := r.Header.Get("Content-Encoding"); c == "gzip" {
		// 	log.Printf("\n%d\n\n", len(b))
		// 	gr, _ := gzip.NewReader(bytes.NewBuffer(b))
		// 	b, err = ioutil.ReadAll(gr)
		// 	log.Printf("\n%d\n\n", len(b))
		// 	gr.Close()
		// 	if err != nil {
		// 		return err
		// 	}
		// }
		b = bytes.ReplaceAll(b, []byte("adsbygoogle"), []byte("xxxxxxx"))
		if index := bytes.Index(b, []byte("slist sec")); index != -1 {
			b = bytes.ReplaceAll(b, []byte("<body>"), []byte("<body><style>a{color:#011;}</style><style>ul.list.sec {display: none;}</style>"))
		}
		// newb := new(bytes.Buffer)
		// gw := gzip.NewWriter(newb)
		// gw.Write(b)
		// gw.Close()
		// b = newb.Bytes()
		body := ioutil.NopCloser(bytes.NewReader(b))
		r.Body = body
		r.ContentLength = int64(len(b))
		r.Header.Set("Content-Length", strconv.Itoa(len(b)))
		// log.Printf("\n%s: \t%+v\n\n", "resp", r)
		return nil
	}

	qs := req.URL.Query()
	dest := qs.Get("dest")
	file := qs.Get("file")
	listen := qs.Get("listen")
	if dest != "" {
		fmt.Println(dest)
		article, err := getContent(dest)
		if err != nil {
			error500(w, err)
			return
		}
		title := article.Title
		content := article.Content
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html;charset=UTF-8")
		toWrite := `<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0" /><title>` +
			title + `</title></head><body><h3>` + title + `</h3><style>body {background-color: black;font-size:` + strconv.Itoa(FONTSIZE) +
			";color:#fff;}</style>\n" + content + `</body></html>`
		w.Write([]byte(toWrite))
	} else if listen != "" {
		fmt.Println(listen)
		out := MP3CACHE[listen]
		if out == "" {
			out = time.Now().Format("20060102150405") + ".mp3"

			outFile := filepath.Join(WEBROOT, out)
			article, err := getContent(listen)
			if err != nil {
				error500(w, err)
				return
			}
			content := article.TextContent
			if getMP3(content, outFile) != nil {
				error500(w, err)
				return
			}
			MP3CACHE[listen] = out
		}
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "text/html;charset=UTF-8")
		mp3 := SCHEME + HOST + ":" + strconv.Itoa(PORT) + `/?file=` + out
		js := SCHEME + HOST + ":" + strconv.Itoa(PORT) + `/?file=ac_quicktime.js`
		toWrite := `<!doctype html><html><body><script type="text/javascript" src="` + js + `"></script><audio id="mp3" controls><source src="` + mp3 + `" type="audio/mpeg"><script>QT_WriteOBJECT("` + mp3 + `","", "","");</script></audio></body></html>`
		w.Write([]byte(toWrite))
	} else if file != "" {
		fmt.Println(file)
		fullPath := filepath.Join(WEBROOT, file)
		buf, err := ioutil.ReadFile(filepath.FromSlash(fullPath))
		if err != nil {
			error500(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		if strings.HasSuffix(file, ".js") {
			w.Header().Set("Content-Type", "text/javascript;charset=UTF-8")
		} else {
			w.Header().Set("Content-Type", "audio/mpeg")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
		w.Write(buf)
	} else {
		rp.ServeHTTP(w, req)
	}

}

func error500(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(err.Error()))
}
func main() {
	BOOKSITE = "https://m.booklink.me/"
	FONTSIZE = 17
	WEBROOT = "/tmp/audio"
	TTS_BASE = "http://tsn.baidu.com/text2audio"
	TTS_SEG_LEN = 500
	TTS_PER = 5118
	TTS_SPD = 10
	TTS_VOL = 8
	UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.90 Safari/537.36"
	HOST = "bj.dujie.name"
	PORT = 8888
	SCHEME = "https://"
	MP3CACHE = make(map[string]string)
	http.HandleFunc("/", defaultHandler)
	http.ListenAndServe("127.0.0.1:9001", nil)
}

func getMP3(content string, out string) error {
	runes := []rune(content)
	length := len(runes)
	total := length
	var chans []chan bool
	mp3s := make(map[int][]byte)
	i := 0
	for length >= 0 {
		c := make(chan bool)
		chans = append(chans, c)
		go func(index int, success chan bool) {
			b := index * TTS_SEG_LEN
			e := (index + 1) * TTS_SEG_LEN
			if e > total {
				e = total
			}
			seq := url.QueryEscape(string(runes[b:e]))
			data := url.Values{}
			// data={'lan':'zh','ie':'UTF-8','spd':10,'tex':urllib.parse.quote(words), 'per': 5118, 'cuid':'baidu_speech_demo','idx':1,'cod':2,'ctp':1,'pdt':1,'vol':8,'pit':5,'_res_tag_':'audio'}
			data.Set("lan", "zh")
			data.Set("ie", "UTF-8")
			data.Set("spd", strconv.Itoa(TTS_SPD))
			data.Set("tex", seq)
			data.Set("per", strconv.Itoa(TTS_PER))
			data.Set("cuid", "baidu_speech_demo")
			data.Set("idx", "1")
			data.Set("cod", "2")
			data.Set("ctp", "1")
			data.Set("pdt", "1")
			data.Set("vol", strconv.Itoa(TTS_VOL))
			data.Set("pit", "5")
			data.Set("_res_tag_", "audio")

			client := &http.Client{}
			req, err := http.NewRequest(http.MethodPost, TTS_BASE, strings.NewReader(data.Encode()))
			if err != nil {
				log.Printf("Error creating tts req %v\n", req)
				c <- false
				return
			}
			resp, err := client.Do(req)

			if err != nil || resp.Header.Get("Content-Type") != "audio/mp3" {
				buf, _ := ioutil.ReadAll(resp.Body)
				log.Printf("Error get tts resp %v\n", string(buf))
				c <- false
				return
			}
			defer resp.Body.Close()
			buf, err := ioutil.ReadAll(resp.Body)
			log.Printf("in thread %d, get buf length %d\n", index, len(buf))
			if err != nil {
				log.Printf("Error reading tts resp %v\n", resp)
				c <- false
				return
			}
			mp3s[index] = buf
			c <- true
		}(i, c)
		i++
		length -= TTS_SEG_LEN
	}
	for _, ch := range chans {
		if !<-ch {
			return errors.New("error getting mp3")
		}
	}
	err := mergeMP3(mp3s, out)
	if err != nil {
		return err
	}
	return nil
}
func getContent(srcPath string) (*readability.Article, error) {
	// Open or fetch web page that will be parsed
	article, buf := getOneArticle(srcPath)
	if article == nil {
		return article, errors.New("null article")
	}
	// if buf == nil {
	// 	return article, nil
	// }
	nextLink := getNextLink(buf)
	if nextLink == "" {
		return article, nil
	}
	r, _ := regexp.Compile(srcPath[strings.LastIndex(srcPath, "/")+1:strings.LastIndex(srcPath, ".")] + `_\d+`)

	for nextLink != "" && !strings.HasSuffix(nextLink, "/") && r.MatchString(nextLink[strings.LastIndex(nextLink, "/")+1:strings.LastIndex(nextLink, ".")]) {
		l, _ := url.Parse(srcPath)
		nl, _ := url.Parse(nextLink)
		nl = l.ResolveReference(nl)
		article1, buf1 := getOneArticle(nl.String())
		if article1 == nil {
			break
		}
		article.Content += article1.Content
		article.TextContent += article1.TextContent
		// if buf1 == nil {
		// 	break
		// }
		nextLink = getNextLink(buf1)
	}

	return article, nil
}

func determineEncoding(r *bufio.Reader) encoding.Encoding {
	bytes, err := r.Peek(1024)
	if err != nil {
		log.Printf("Fetcher error:%v\n", err)
		return unicode.UTF8
	}
	e, _, _ := charset.DetermineEncoding(bytes, "")
	return e
}

func getOneArticle(dest string) (*readability.Article, []byte) {
	var (
		pageURL   string
		srcReader io.Reader
	)

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
	e := determineEncoding(bodyReader)
	utf8Reader := transform.NewReader(bodyReader, e.NewDecoder())
	srcReader = utf8Reader
	pageURL = dest
	defer resp.Body.Close()
	buf := bytes.NewBuffer(nil)
	tee := io.TeeReader(srcReader, buf)
	article, err := readability.FromReader(tee, pageURL)
	if err != nil {
		return nil, nil
	}
	return &article, buf.Bytes()
}

func getNextLink(buf []byte) string {
	doc, err := html.Parse(bytes.NewReader(buf))
	if err != nil {
		return ""
	}
	nextURLs := make(map[string]string)
	var f func(*html.Node, map[string]string)
	f = func(n *html.Node, m map[string]string) {
		if n.Type == html.ElementNode && n.Data == "a" {
			if n.FirstChild != nil && (strings.TrimSpace(n.FirstChild.Data) == "下一页" || strings.TrimSpace(n.FirstChild.Data) == "下一章") {
				for _, a := range n.Attr {
					if a.Key == "href" {
						m[n.FirstChild.Data] = strings.TrimSpace(a.Val)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c, m)
		}
	}
	f(doc, nextURLs)
	nextLink := nextURLs["下一章"]
	if nextURLs["下一页"] != "" {
		nextLink = nextURLs["下一页"]
	}
	return nextLink
}

func mergeMP3(infiles map[int][]byte, out string) error {
	outfile, err := os.Create(filepath.FromSlash(out))
	defer outfile.Close()
	if err != nil {
		return err
	}
	for i := 0; i < len(infiles); i++ {

		outfile.Write(infiles[i])
		// for {
		// 	// Read the next frame from the input buf.
		// 	frame := mp3lib.NextFrame(bytes.NewReader(infiles[i]))
		// 	if frame == nil {
		// 		break
		// 	}
		// 	// Write the frame to the output file.
		// 	_, err := outfile.Write(frame.RawBytes)
		// 	if err != nil {
		// 		return err
		// 	}
		// }
	}
	return nil
}