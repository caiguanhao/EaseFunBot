package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"golang.org/x/net/html"
)

type (
	netEaseTime struct {
		time.Time
	}

	netEasePost struct {
		ID        string      `json:"postid"`
		Title     string      `json:"title"`
		CreatedAt netEaseTime `json:"ptime"`
	}

	netEasePosts []netEasePost

	netEaseImage struct {
		Ref string `json:"ref"`
		URL string `json:"src"`
	}

	netEaseDocument struct {
		Images []netEaseImage `json:"img"`
		Body   string         `json:"body"`
	}
)

const (
	linkFormat   = "/02"
	errorMessage = "Something went wrong."
	helpMessage  = `You can:
/list recent posts`
)

var (
	netEaseTimeZone = time.FixedZone("CST", 60*60*8)
)

func (posts netEasePosts) String() (out string) {
	for i, post := range posts {
		if i > 0 {
			out = out + "\n"
		}
		out = out + post.CreatedAt.Format(linkFormat) + " " + strings.Replace(post.Title, "每日易乐:", "", 1)
	}
	return
}

func (t *netEaseTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	_t, err := time.ParseInLocation(`"2006-01-02 15:04:05"`, string(data), netEaseTimeZone)
	*t = netEaseTime{_t}
	return err
}

func getPosts() (netEasePosts, error) {
	resp, err := http.Get("https://c.m.163.com/nc/subscribe/list/T1454661781964/all/0-25.html")
	if err != nil {
		return nil, err
	}
	var posts struct {
		Posts netEasePosts `json:"tab_list"`
	}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&posts)
	if err != nil {
		return nil, err
	}
	return posts.Posts, nil
}

func getPost(id string, chatId int64) (messages []tgbotapi.Chattable, _err error) {
	resp, err := http.Get(fmt.Sprintf("https://c.m.163.com/nc/article/%s/full.html", id))
	if _err = err; _err != nil {
		return
	}
	var doc map[string]netEaseDocument
	dec := json.NewDecoder(resp.Body)
	if _err = dec.Decode(&doc); _err != nil {
		return
	}

	imageMap := map[string]string{}
	for _, img := range doc[id].Images {
		ref := strings.Replace(img.Ref, "<!--", "", -1)
		ref = strings.Replace(ref, "-->", "", -1)
		imageMap[ref] = img.URL
	}

	n, err := html.Parse(strings.NewReader(doc[id].Body))
	if _err = err; _err != nil {
		return
	}

	var imageCaption string
	var f func(*html.Node)
	f = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			imageCaption = strings.TrimSpace(n.Data)
		case html.CommentNode:
			imageUrl, ok := imageMap[n.Data]
			if !ok {
				break
			}
			if strings.HasSuffix(imageUrl, "gif") {
				msg := tgbotapi.NewDocumentUpload(chatId, nil)
				msg.FileID = imageUrl
				msg.UseExisting = true
				msg.Caption = imageCaption
				messages = append(messages, msg)
			} else {
				msg := tgbotapi.NewPhotoUpload(chatId, nil)
				msg.FileID = imageUrl
				msg.UseExisting = true
				msg.Caption = imageCaption
				messages = append(messages, msg)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)

	return
}

func main() {
	bot, err := tgbotapi.NewBotAPI(os.Getenv("BOTAPI"))
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false

	log.Printf("Started %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Panic(err)
	}

	posts, err := getPosts()
	if err != nil {
		log.Panic(err)
	}

	for update := range updates {
		if update.Message == nil {
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		target := update.Message.Chat.ID

		switch update.Message.Text {
		case "/list":
			bot.Send(tgbotapi.NewMessage(target, posts.String()))
		case "\xF0\x9F\x91\x8D":
			bot.Send(tgbotapi.NewMessage(target, "Thank you."))
		default:
			t, err := time.Parse(linkFormat, update.Message.Text)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(target, helpMessage))
				break
			}
			for _, post := range posts {
				if post.CreatedAt.Day() != t.Day() {
					continue
				}
				messages, err := getPost(post.ID, target)
				if err != nil {
					log.Println(err)
					bot.Send(tgbotapi.NewMessage(target, errorMessage))
					break
				}
				for _, msg := range messages {
					for i := 0; i < 3; i++ {
						_, err := bot.Send(msg)
						if err == nil {
							break
						} else {
							log.Println(err)
						}
					}
				}
				break
			}
		}
	}
}
