package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
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

	subscriber struct {
		UserID int64  `json:"user_id"`
		PostID string `json:"post_id"`
	}
)

const (
	linkFormat   = "/02"
	errorMessage = "Something went wrong."
	helpMessage  = `You can:

/list recent posts
/subscribe for new posts
/unsubscribe if you've subscribed`
)

var (
	netEaseTimeZone = time.FixedZone("CST", 60*60*8)

	dataFile string

	botdata struct {
		TotalLikes  int          `json:"total_likes"`
		Subscribers []subscriber `json:"subscribers"`
	}

	bot *tgbotapi.BotAPI

	latestPosts netEasePosts
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
	defer resp.Body.Close()
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

func getPost(id string) (messages [][2]string, _err error) {
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
			messages = append(messages, [2]string{imageUrl, imageCaption})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)

	return
}

func sendPostsToSubscribers() {
	var err error
	latestPosts, err = getPosts()
	if err != nil {
		log.Panic(err)
		return
	}
	diff := time.Now().Sub(latestPosts[0].CreatedAt.Time)
	if diff < 0 || diff > 10*time.Minute {
		return
	}
	allSent := true
	for i := range botdata.Subscribers {
		if botdata.Subscribers[i].PostID != latestPosts[0].ID {
			allSent = false
		}
	}
	if allSent {
		return
	}
	messages, err := getPost(latestPosts[0].ID)
	if err != nil {
		log.Panic(err)
		return
	}
	for i := range botdata.Subscribers {
		if botdata.Subscribers[i].PostID == latestPosts[0].ID {
			continue
		}
		err = sendMessages(messages, botdata.Subscribers[i].UserID)
		if err != nil {
			log.Println(err)
		}
		botdata.Subscribers[i].PostID = latestPosts[0].ID
		write()
	}
}

func sendMessages(messages [][2]string, target int64) (err error) {
	for _, msg := range messages {
		imageUrl, imageCaption := msg[0], msg[1]
		for i := 0; i < 2; i++ {
			_, err = bot.Send(newMessage(target, imageUrl, imageCaption))
			if err != nil && strings.Contains(err.Error(), "wrong file identifier/HTTP URL specified") {
				imageUrl += "?new"
				_, err = bot.Send(newMessage(target, imageUrl, imageCaption))
			}
			if err == nil {
				break
			}
		}
		if err != nil {
			bot.Send(tgbotapi.NewMessage(target, "Error: "+err.Error()))
			log.Println(err)
			return
		}
	}
	bot.Send(tgbotapi.NewMessage(target, "End of the post. You can /list other posts or visit /help."))
	return
}

func newMessage(target int64, _url, caption string) tgbotapi.Chattable {
	u, err := url.Parse(_url)
	if err == nil && strings.HasSuffix(u.Path, "gif") {
		msg := tgbotapi.NewDocumentUpload(target, nil)
		msg.FileID = _url
		msg.UseExisting = true
		msg.Caption = caption
		return msg
	}
	msg := tgbotapi.NewPhotoUpload(target, nil)
	msg.FileID = _url
	msg.UseExisting = true
	msg.Caption = caption
	return msg
}

func read() error {
	defer func() {
		if botdata.Subscribers == nil {
			botdata.Subscribers = []subscriber{}
		}
	}()
	b, err := ioutil.ReadFile(dataFile)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &botdata)
}

func write() error {
	b, err := json.MarshalIndent(botdata, "", "  ")
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(dataFile, b, 0644); err != nil {
		return err
	}
	return nil
}

func subscribe(target int64, isSubscribe bool) (ok bool) {
	for i := len(botdata.Subscribers) - 1; i > -1; i-- {
		if botdata.Subscribers[i].UserID != target {
			continue
		}
		if isSubscribe {
			return
		} else {
			botdata.Subscribers = append(botdata.Subscribers[:i], botdata.Subscribers[i+1:]...)
			ok = true
		}
	}
	if isSubscribe {
		botdata.Subscribers = append(botdata.Subscribers, subscriber{UserID: target})
		ok = true
	}
	write()
	return
}

func init() {
	flag.StringVar(&dataFile, "data", "botdata.json", "data file location")
}

func main() {
	flag.Parse()

	var err error
	bot, err = tgbotapi.NewBotAPI(os.Getenv("BOTAPI"))
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false

	log.Printf("Started %s", bot.Self.UserName)

	read()

	go func() {
		for {
			sendPostsToSubscribers()
			time.Sleep(10 * time.Second)
		}
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates, err := bot.GetUpdatesChan(u)
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
			bot.Send(tgbotapi.NewMessage(target, latestPosts.String()))
		case "/subscribe":
			if subscribe(target, true) {
				bot.Send(tgbotapi.NewMessage(target, "Subscribed!"))
			} else {
				bot.Send(tgbotapi.NewMessage(target, "You've already subscribed. No need to subscribe again."))
			}
		case "/unsubscribe":
			if subscribe(target, false) {
				bot.Send(tgbotapi.NewMessage(target, "Unsubscribed!"))
			} else {
				bot.Send(tgbotapi.NewMessage(target, "You don't have any subscription to unsubscribe."))
			}
		case "\xF0\x9F\x91\x8D":
			botdata.TotalLikes += 1
			write()
			bot.Send(tgbotapi.NewMessage(target, "Thank you."))
		default:
			t, err := time.Parse(linkFormat, update.Message.Text)
			if err != nil {
				bot.Send(tgbotapi.NewMessage(target, helpMessage))
				break
			}
			match := false
			for _, post := range latestPosts {
				if post.CreatedAt.Day() != t.Day() {
					continue
				}
				match = true
				messages, err := getPost(post.ID)
				if err == nil {
					sendMessages(messages, target)
				} else {
					log.Println(err)
					bot.Send(tgbotapi.NewMessage(target, errorMessage))
				}
				break
			}
			if !match {
				bot.Send(tgbotapi.NewMessage(target, "No such post."))
			}
		}
	}
}
