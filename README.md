# adidas-crawling
Online Technical Test for the post of "Crawling Engineer (Golang)"

# Installation
```
go get -t -d github.com/tebeka/selenium
go get go.mongodb.org/mongo-driver/mongo
go get go.mongodb.org/mongo-driver/mongo/options
go get -u github.com/xuri/excelize/v2
```

# Configuration
```
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
```