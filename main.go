package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tebeka/selenium"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ProductURL struct {
	Category string `json:"category"`
	PageNo   int    `json:"pageno"`
	URL      string `json:"url"`
}

type ColorOption struct {
	Path  string `json:"path"`
	Color string `json:"color"`
}

type ReviewSummary struct {
	Rating          float64 `json:"rating"`
	NumberOfReviews int     `json:"number_of_reviews"`
	RecommendedRate string  `json:"recommended_rate"`
	Fit             string  `json:"fit"`
	Length          string  `json:"length"`
	Quality         string  `json:"quality"`
	Comfort         string  `json:"comfort"`
}

type Review struct {
	Rating      float64 `json:"rating"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Date        string  `json:"date"`
	ReviewId    string  `json:"reviewId"`
}

type CoordinatedProduct struct {
	Title         string `json:"title"`
	Price         string `json:"price"`
	Path          string `json:"path"`
	ProductNumber string `json:"product_number"`
	ProductURL    string `json:"product_page_url"`
}

type SpecialDescription struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type Media struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type Product struct {
	ProductURL          string                         `json:"product_url"`
	Breadcrumbs         []string                       `json:"breadcrumbs"`
	Category            string                         `json:"category"`
	Title               string                         `json:"title"`
	Price               string                         `json:"price"`
	AvailableColors     []ColorOption                  `json:"available_colors"`
	AvailableSizes      []string                       `json:"available_sizes"`
	Media               []Media                        `json:"media"`
	CoordinatedProducts []CoordinatedProduct           `json:"coordinated_products"`
	DescriptionHeading  string                         `json:"description_heading"`
	DescriptionTitle    string                         `json:"description_title"`
	Description         string                         `json:"description"`
	Specifications      []string                       `json:"specifications"`
	SpecialDescription  []SpecialDescription           `json:"special_description"`
	SizeChart           map[string][]map[string]string `json:"size_chart"`
	SizeRemarks         []string                       `json:"size_remarks"`
	ReviewSummary       ReviewSummary                  `json:"review_summary"`
	Reviews             []Review                       `json:"reviews"`
	Tags                []string                       `json:"tags"`
}

// Other types omitted for brevity

const (
	seleniumPath         = "/path/to/selenium-server.jar"
	chromeDriverPath     = "/path/to/chromedriver"
	port                 = 4444
	numWorkers           = 10
	mongoURI             = "mongodb://127.0.0.1:27017"
	dbName               = "adidas"
	productURLCollection = "product_urls"
	productCollection    = "products"
)

func main() {
	log.Println("Crawling starting...")

	opts := []selenium.ServiceOption{
		selenium.ChromeDriver(chromeDriverPath),
		selenium.Output(nil), // Output debug info to stderr
	}
	service, err := selenium.NewSeleniumService(seleniumPath, port, opts...)
	if err != nil {
		log.Fatalf("Error starting the Selenium server: %v", err)
	}
	defer service.Stop()

	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer func() {
		if err := client.Disconnect(context.TODO()); err != nil {
			log.Fatalf("Failed to disconnect from MongoDB: %v", err)
		}
	}()

	productUrlCollection := client.Database(dbName).Collection(productURLCollection)
	productCollection := client.Database(dbName).Collection(productCollection)

	// Check if product_urls collection is empty
	productURLCount, err := productUrlCollection.CountDocuments(context.Background(), bson.M{})
	if err != nil {
		log.Fatalf("Failed to count documents in product_urls collection: %v", err)
	}

	caps := selenium.Capabilities{
		"browserName": "chrome",
		"chromeOptions": map[string]interface{}{
			"args": []string{"--start-fullscreen"},
		},
	}

	if productURLCount == 0 {

		productUrlChan := make(chan string)
		var wg sync.WaitGroup

		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				processURLs(productUrlChan, caps, productUrlCollection)
			}()
		}

		wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
		if err != nil {
			log.Fatalf("Error connecting to the WebDriver server: %v", err)
		}
		defer wd.Quit()

		if err := wd.Get("https://shop.adidas.jp/men/"); err != nil {
			log.Fatalf("Failed to load page: %v", err)
		}

		time.Sleep(5 * time.Second)

		categoryElems, err := wd.FindElements(selenium.ByCSSSelector, ".lpc-ukLocalNavigation_itemList li a")
		if err != nil {
			log.Fatalf("Failed to find category elements: %v", err)
		}

		var categories []string
		for _, elem := range categoryElems {
			href, err := elem.GetAttribute("href")
			if err != nil {
				log.Printf("Failed to get href attribute: %v", err)
				continue
			}
			if href != "" {
				fullURL := "https://shop.adidas.jp" + href
				categories = append(categories, fullURL)
			}
		}

		for key, category := range categories {
			if key == 1 {
				if err := wd.Get(category); err != nil {
					log.Fatalf("Failed to load category page: %v", err)
				}

				pageCount := getPageCount(wd)

				for i := 1; i <= pageCount; i++ {
					pageURL := fmt.Sprintf("%s&page=%d", category, i)
					productUrlChan <- pageURL
				}
			}
		}

		close(productUrlChan)
		wg.Wait()
	}

	filter := bson.M{}
	findOptions := options.Find()
	findOptions.SetLimit(300)

	cursor, err := productUrlCollection.Find(context.Background(), filter, findOptions)
	if err != nil {
		log.Fatalf("Failed to find documents: %v", err)
	}
	defer cursor.Close(context.Background())

	var results []ProductURL
	if err = cursor.All(context.Background(), &results); err != nil {
		log.Fatalf("Failed to iterate over cursor: %v", err)
	}

	if len(results) != 0 {
		productChan := make(chan string)
		var wg sync.WaitGroup

		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				processProduct(productChan, caps, productCollection)
			}()
		}

		for _, result := range results {
			productChan <- result.URL
		}

		close(productChan)
		wg.Wait()
	}

	log.Println("Crawling finished!")
}

func processURLs(productUrlChan chan string, caps selenium.Capabilities, collection *mongo.Collection) {
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
	if err != nil {
		log.Fatalf("Error connecting to the WebDriver server: %v", err)
	}
	defer wd.Quit()

	for url := range productUrlChan {
		if err := wd.Get(url); err != nil {
			log.Printf("Failed to load page URL: %v", err)
			continue
		}

		closeModals(wd)
		scrollToBottom(wd)
		time.Sleep(5 * time.Second)

		productElems, err := wd.FindElements(selenium.ByCSSSelector, ".articleDisplayCard-children a.image_link")
		if err != nil {
			log.Printf("Failed to find product elements: %v", err)
			continue
		}

		pageNo := extractPageNumber(url)
		category := extractCategory(url)
		if pageNo == -1 || category == "" {
			log.Printf("Failed to extract page number from URL: %s", url)
			continue
		}

		for _, elem := range productElems {
			href, err := elem.GetAttribute("href")
			if err != nil || href == "" {
				continue
			}

			fullURL := "https://shop.adidas.jp" + href

			_, err = collection.InsertOne(context.TODO(), ProductURL{Category: category, PageNo: pageNo, URL: fullURL})
			if err != nil {
				log.Printf("Failed to insert document: %v", err)
			}
		}
	}
}

func extractPageNumber(url string) int {
	re := regexp.MustCompile(`page=(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) < 2 {
		return -1
	}

	pageNo, err := strconv.Atoi(matches[1])
	if err != nil {
		log.Printf("Failed to convert page number to integer: %v", err)
		return -1
	}

	return pageNo
}

func extractCategory(url string) string {
	re := regexp.MustCompile(`category=([^&]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) < 2 {
		return ""
	}

	return matches[1]
}

func getPageCount(wd selenium.WebDriver) int {
	pageCount := 1
	pageTotalElem, err := wd.FindElement(selenium.ByCSSSelector, ".pageTotal")
	if err == nil && pageTotalElem != nil {
		pageTotalText, err := pageTotalElem.Text()
		if err == nil {
			pageTotal, err := strconv.Atoi(strings.TrimSpace(pageTotalText))
			if err == nil {
				pageCount = pageTotal
			}
		}
	}
	return pageCount
}

func scrollToBottom(wd selenium.WebDriver) {
	for {
		_, err := wd.ExecuteScript("window.scrollBy(0, 1000);", nil)
		if err != nil {
			log.Fatalf("Failed to scroll: %v", err)
		}

		time.Sleep(5 * time.Second)

		scrollHeight, err := wd.ExecuteScript("return document.documentElement.scrollHeight;", nil)
		if err != nil {
			log.Fatalf("Failed to get scroll height: %v", err)
		}

		clientHeight, err := wd.ExecuteScript("return document.documentElement.clientHeight;", nil)
		if err != nil {
			log.Fatalf("Failed to get client height: %v", err)
		}

		scrollTop, err := wd.ExecuteScript("return document.documentElement.scrollTop;", nil)
		if err != nil {
			log.Fatalf("Failed to get scroll top: %v", err)
		}

		if scrollTop.(float64)+clientHeight.(float64) >= scrollHeight.(float64) {
			break
		}
	}
}

func closeModals(wd selenium.WebDriver) {
	closeButtons, err := wd.FindElements(selenium.ByCSSSelector, ".modal .boxClose")
	if err != nil {
		log.Printf("Failed to find modal close buttons: %v", err)
	}

	for _, closeButton := range closeButtons {
		if err := closeButton.Click(); err == nil {
			log.Println("Modal closed")
		}
	}
}

func processProduct(urlChan <-chan string, caps selenium.Capabilities, productsCollection *mongo.Collection) {
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
	if err != nil {
		log.Printf("Error connecting to the WebDriver server: %v", err)
		return
	}
	defer wd.Quit()

	for url := range urlChan {
		product := scrapeProduct(wd, url)
		if product != nil {
			// Insert product into MongoDB
			_, err := productsCollection.InsertOne(context.Background(), product)
			if err == nil {
				log.Printf("Inserted product: %s", product.ProductURL)
			}
		}
	}
}

func scrapeProduct(wd selenium.WebDriver, url string) *Product {
	product := &Product{ProductURL: url}

	baseURL := "https://shop.adidas.jp"

	if err := wd.Get(url); err != nil {
		log.Printf("Failed to load page: %v", err)
	}

	time.Sleep(5 * time.Second)

	_, err := wd.FindElement(selenium.ByCSSSelector, ".article_image_wrapper")

	if err == nil {
		script := `
		var element = document.querySelector('.article_image_wrapper');
		if (element) {
			element.classList.add('isExpand');
		}
	`
		_, scriptErr := wd.ExecuteScript(script, nil)
		if scriptErr != nil {
			log.Printf("Failed to add 'isExpand' class to 'article_image_wrapper' element: %v", scriptErr)
		}
	}

	closeModals(wd)
	scrollToBottom(wd)

	// Wait for the page to load completely
	time.Sleep(5 * time.Second)

	// Product URL
	product.ProductURL = url

	// =============================== Breadcrumb Start =========================
	breadcrumbElements, err := wd.FindElements(selenium.ByCSSSelector, ".breadcrumbListItem a")
	if err == nil {

		for key, breadcrumbElement := range breadcrumbElements {
			text, err := breadcrumbElement.Text()
			if err == nil && text != "" {
				if key != 0 && key != 1 {
					product.Breadcrumbs = append(product.Breadcrumbs, text)
				}
			}
		}
	}
	// =============================== Breadcrumb End =========================

	// =============================== Category Start =========================
	categoryNameElement, err := wd.FindElement(selenium.ByCSSSelector, ".categoryName")
	if err == nil {
		categoryName, err := categoryNameElement.Text()
		if err == nil {
			product.Category = categoryName
		}
	}
	// =============================== Category End =========================

	// =============================== Item Title Start =========================
	itemTitleElement, err := wd.FindElement(selenium.ByCSSSelector, ".itemTitle")
	if err == nil {
		itemTitle, err := itemTitleElement.Text()
		if err == nil {
			product.Title = itemTitle
		}
	}
	// =============================== Item Title End =========================

	// =============================== Item Price Start =========================
	priceElement, err := wd.FindElement(selenium.ByCSSSelector, ".price-value")
	if err == nil {
		price, err := priceElement.Text()
		if err == nil {
			product.Price = price
		}
	}
	// =============================== Item Price End =========================

	// ============================== Color Start =============================
	colorOptionElements, err := wd.FindElements(selenium.ByCSSSelector, ".selectable-image-group .selectableImageListItem")
	if err != nil {
		log.Fatalf("Failed to find color option elements: %v", err)
	}

	for _, element := range colorOptionElements {
		imgElement, err := element.FindElement(selenium.ByTagName, "img")
		if err != nil {
			continue
		}
		imageSrc, _ := imgElement.GetAttribute("src")
		color, _ := imgElement.GetAttribute("alt")

		if imageSrc != "" && color != "" {
			imageURL := baseURL + imageSrc
			product.AvailableColors = append(product.AvailableColors, ColorOption{
				Path:  imageURL,
				Color: color,
			})
		}
	}
	// ============================== Color End =============================

	// ============================== Available Size Start =============================
	sizeElements, err := wd.FindElements(selenium.ByCSSSelector, ".sizeSelectorList .sizeSelectorListItemButton")
	if err != nil {
		log.Fatalf("Failed to find size elements: %v", err)
	}

	for _, sizeElement := range sizeElements {
		sizeText, err := sizeElement.Text()
		if err == nil && sizeText != "" {
			product.AvailableSizes = append(product.AvailableSizes, sizeText)
		}
	}
	// ============================== Available Size End =============================

	// ============================== Image URL Start =============================
	imageElements, err := wd.FindElements(selenium.ByCSSSelector, ".article_image_wrapper img.test-img")
	if err != nil {
		log.Fatalf("Failed to find image elements: %v", err)
	}

	for _, imgElem := range imageElements {
		imgSrc, err := imgElem.GetAttribute("src")
		if err != nil {
			log.Fatalf("Failed to get image src: %v", err)
		}
		product.Media = append(product.Media, Media{
			Path: baseURL + imgSrc,
			Type: "image",
		})
	}

	videoElements, err := wd.FindElements(selenium.ByCSSSelector, ".pdp-article-video-wrap video")
	if err != nil {
		log.Fatalf("Failed to find video elements: %v", err)
	}

	for _, videoElem := range videoElements {
		videoSrc, err := videoElem.GetAttribute("src")
		if err != nil {
			log.Fatalf("Failed to get video src: %v", err)
		}
		product.Media = append(product.Media, Media{
			Path: baseURL + videoSrc,
			Type: "video",
		})
	}
	// ============================== Image URL End ====================================

	// ============================== Coordinated Start =============================
	productElements, err := wd.FindElements(selenium.ByCSSSelector, ".coordinateItems .carouselListitem")
	if err == nil {
		for _, productElement := range productElements {
			var coorProduct CoordinatedProduct

			// Get product name
			productNameElement, err := productElement.FindElement(selenium.ByCSSSelector, ".coordinate_image img")
			if err == nil {
				productName, err := productNameElement.GetAttribute("alt")
				if err == nil {
					coorProduct.Title = productName
				}
			}

			// Get price
			priceElement, err := productElement.FindElement(selenium.ByCSSSelector, ".price-value.test-price-value")
			if err == nil {
				price, err := priceElement.Text()
				if err == nil {
					coorProduct.Price = price
				}
			}

			// Get image URL
			imageURL, err := productNameElement.GetAttribute("src")
			if err == nil {
				coorProduct.Path = baseURL + imageURL
			}

			// // Extract product number from image URL
			urlParts := strings.Split(imageURL, "/")
			if len(urlParts) > 3 {
				productNumber := urlParts[3]
				coorProduct.ProductNumber = productNumber
			}

			// // // Get product page URL
			productURL := "https://shop.adidas.jp/products/" + coorProduct.ProductNumber
			coorProduct.ProductURL = productURL

			product.CoordinatedProducts = append(product.CoordinatedProducts, coorProduct)
		}
	}
	// ============================== Coordinated End =============================

	// ============================== Description Start =============================
	DescriptionHeadingElement, err := wd.FindElement(selenium.ByCSSSelector, ".heading.itemName.test-commentItem-topHeading")
	if err == nil {
		descriptionHeading, err := DescriptionHeadingElement.Text()
		if err == nil {
			product.DescriptionHeading = descriptionHeading
		}
	}

	descriptionTitleElement, err := wd.FindElement(selenium.ByCSSSelector, ".heading.itemFeature.test-commentItem-subheading")
	if err == nil {
		descriptionTitle, err := descriptionTitleElement.Text()
		if err == nil {
			product.DescriptionTitle = descriptionTitle
		}
	}

	description, err := wd.FindElement(selenium.ByCSSSelector, ".description.clearfix.test-descriptionBlock .description_part.details.test-itemComment-descriptionPart .commentItem-mainText.test-commentItem-mainText")
	if err == nil {
		descriptionText, err := description.Text()
		if err == nil {
			product.Description = descriptionText
		}
	}

	specificationItems, err := wd.FindElements(selenium.ByCSSSelector, ".articleFeatures.description_part .articleFeaturesItem")
	if err == nil {
		for _, item := range specificationItems {
			itemText, err := item.Text()
			if err == nil {
				product.Specifications = append(product.Specifications, itemText)
			}
		}
	}
	// ============================== Description End =================================

	// ============================== Specific Description Start =============================
	contentElements, err := wd.FindElements(selenium.ByCSSSelector, ".contents .content")
	if err == nil {
		var specialDescription SpecialDescription

		for _, content := range contentElements {
			titleElement, titleErr := content.FindElement(selenium.ByCSSSelector, ".tecTextTitle")
			imgAltElement, imgAltErr := content.FindElement(selenium.ByCSSSelector, "div.item_part.illustration img")

			if titleErr == nil && imgAltErr == nil {
				title, _ := titleElement.Text()
				imgAlt, _ := imgAltElement.GetAttribute("alt")

				if title != "" && imgAlt != "" {
					specialDescription.Title = title
					specialDescription.Description = imgAlt
				}
			}
			product.SpecialDescription = append(product.SpecialDescription, specialDescription)
		}
	}
	// ============================== Specific Description End =============================

	// ==================== Size Chart Start ==========================
	headerElems, err := wd.FindElements(selenium.ByCSSSelector, ".sizeChartTable thead .sizeChartTHeaderCell")
	if err != nil {
		log.Fatalf("Failed to find header elements: %v", err)
	}
	var headers []string
	for _, elem := range headerElems {
		text, err := elem.Text()
		if err != nil {
			log.Fatalf("Failed to get header text: %v", err)
		}
		if text != "" {
			headers = append(headers, text)
		}
	}

	// Extract size keys
	sizeKeysElems, err := wd.FindElements(selenium.ByCSSSelector, ".sizeChartTable tbody .sizeChartTRow:nth-of-type(1) .sizeChartTCell span")
	if err != nil {
		log.Fatalf("Failed to find size key elements: %v", err)
	}
	var sizeKeys []string
	for _, elem := range sizeKeysElems {
		text, err := elem.Text()
		if err != nil {
			log.Fatalf("Failed to get size key text: %v", err)
		}
		sizeKeys = append(sizeKeys, text)
	}

	// Extract size chart data dynamically
	sizeChart := make(map[string][]map[string]string)
	for i, header := range headers {
		sizeChart[header] = make([]map[string]string, len(sizeKeys))
		rows, err := wd.FindElements(selenium.ByCSSSelector, fmt.Sprintf(".sizeChartTable tbody .sizeChartTRow:nth-of-type(%d) .sizeChartTCell span", i+2))
		if err != nil {
			log.Fatalf("Failed to find row elements: %v", err)
		}
		for j, row := range rows {
			text, err := row.Text()
			if err != nil {
				log.Fatalf("Failed to get row text: %v", err)
			}
			sizeChart[header][j] = map[string]string{sizeKeys[j]: text}
		}
	}

	product.SizeChart = sizeChart

	remarkElements, err := wd.FindElements(selenium.ByCSSSelector, ".remarkList.test-remarkList .sizeDescriptionRemark")
	if err == nil {
		for _, remarkElement := range remarkElements {
			remarkText, err := remarkElement.Text()
			if err == nil && remarkText != "" {
				product.SizeRemarks = append(product.SizeRemarks, remarkText)
			}
		}
	}
	// ==================== Size Chart End ==========================

	// ==================== Review Summary Start =======================
	var reviewSummary ReviewSummary

	ratingElement, err := wd.FindElement(selenium.ByCSSSelector, ".BVRRRating.BVRRRatingNormal.BVRRRatingOverall .BVRRRatingNormalOutOf .BVRRRatingNumber")
	if err == nil {
		totalRating, err := ratingElement.Text()
		if err == nil {
			convertedRating, err := strconv.ParseFloat(totalRating, 64)
			if err == nil {
				reviewSummary.Rating = convertedRating
			} else {
				reviewSummary.Rating = 0.0
			}
		} else {
			reviewSummary.Rating = 0.0
		}
	}

	numberOfRatingElement, err := wd.FindElement(selenium.ByCSSSelector, ".BVRRQuickTakeCustomWrapper .BVRRBuyAgainTotal")
	if err == nil {
		numberOfRating, err := numberOfRatingElement.Text()
		if err == nil {
			convert, err := strconv.Atoi(numberOfRating)
			if err == nil {
				reviewSummary.NumberOfReviews = convert
			}
		} else {
			reviewSummary.NumberOfReviews = 0
		}
	}

	recommededElement, err := wd.FindElement(selenium.ByCSSSelector, ".BVRRQuickTakeCustomWrapper .BVRRBuyAgainPercentage")
	if err == nil {
		recommeded_percentage, err := recommededElement.Text()
		if err == nil {
			reviewSummary.RecommendedRate = recommeded_percentage
		} else {
			reviewSummary.RecommendedRate = "0.00%"
		}
	}

	summaryElements, err := wd.FindElements(selenium.ByCSSSelector, ".BVRRSecondaryRatingsContainer .BVRRRatingRadioImage img")
	if err == nil {
		for key, overAll := range summaryElements {
			overAllText, err := overAll.GetAttribute("title")
			if err == nil {
				switch key {
				case 0:
					reviewSummary.Fit = overAllText
				case 1:
					reviewSummary.Length = overAllText
				case 2:
					reviewSummary.Quality = overAllText
				case 3:
					reviewSummary.Comfort = overAllText
				}
			}
		}
	}

	product.ReviewSummary = reviewSummary
	// ==================== Review Summary End =================

	// ==================== Review Start =======================
	reviewDiv, err := wd.FindElements(selenium.ByCSSSelector, ".BVRRDisplayContent .BVRRDisplayContentBody .BVRRContentReview") // BVRRReviewDisplayStyle5
	if err == nil {
		for _, review := range reviewDiv {
			var reviewInfo Review

			// Get Rating Value
			review_rating := 0.0
			ratingValueElement, err := review.FindElement(selenium.ByCSSSelector, ".BVRRReviewDisplayStyle5Header .BVRRRatingNormalImage img")
			if err == nil {
				ratingValueText, err := ratingValueElement.GetAttribute("title")
				ratingText := strings.Split(ratingValueText, "/")
				if err == nil && ratingValueText != "" {
					convertedRating, err := strconv.ParseFloat(strings.Trim(ratingText[1], " "), 64)
					if err == nil {
						review_rating = convertedRating
					}
				}
			}
			reviewInfo.Rating = review_rating

			// Get Review Date
			review_date := ""
			reviewDateElement, err := review.FindElement(selenium.ByCSSSelector, ".BVRRReviewDateContainer meta")
			if err == nil {
				reviewDateText, err := reviewDateElement.GetAttribute("content")
				if err == nil && reviewDateText != "" {
					review_date = reviewDateText
				}
			}
			reviewInfo.Date = review_date

			// Get Review Title
			review_title := ""
			titleText, err := review.FindElement(selenium.ByCSSSelector, ".BVRRReviewTitleContainer .BVRRReviewTitle")
			if err == nil {
				reviewTitle, err := titleText.Text()
				if err == nil && reviewTitle != "" {
					review_title = reviewTitle
				}
			}
			reviewInfo.Title = review_title

			// Get Review Comment
			review_description := ""
			reviewComment, err := review.FindElement(selenium.ByCSSSelector, ".BVRRReviewTextContainer .BVRRReviewText")
			if err == nil {
				commentText, err := reviewComment.Text()
				if err == nil && commentText != "" {
					review_description = commentText
				}
			}
			reviewInfo.Description = review_description

			// Get Review Author
			review_id := ""
			reviewId, err := review.FindElement(selenium.ByCSSSelector, ".BVRRUserNicknameContainer .BVRRUserNickname .BVRRNickname")
			if err == nil {
				authorText, err := reviewId.Text()
				if err == nil && authorText != "" {
					review_id = authorText
				}
			}
			reviewInfo.ReviewId = review_id

			product.Reviews = append(product.Reviews, reviewInfo)
		}
	}
	// ==================== Review End =========================

	// ==================== Tags Start ==========================
	tagElements, err := wd.FindElements(selenium.ByCSSSelector, ".itemTagsPosition a")
	if err == nil {
		for _, tagElement := range tagElements {
			tag, err := tagElement.Text()
			if err == nil && tag != "" {
				product.Tags = append(product.Tags, tag)
			}
		}
	}
	// ==================== Tags End ==========================

	return product
}
