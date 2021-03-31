package main

import (
	"flag"
	"github.com/gin-gonic/gin"
	"net/http"
	"path/filepath"
	"strings"
)

func main() {
	host := flag.String("host", "127.0.0.1", "listen host")
	port := flag.String("port", "8019", "listen port")

	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": true,
			"msg":    "pong",
		})
	})

	// Get filePath by operator info
	r.GET("/raw", func(c *gin.Context) {

		operator := c.Query("operator")
		version := c.Query("version")

		fileDir:= "./community-operators/"+ operator + "/" + version
		filepath, err := filepath.Glob(filepath.Join(fileDir, "*"))
		if err != nil {
			c.JSON(201, gin.H{
				"filepath": nil,
			})
		} else if filepath == nil {
			c.JSON(201, gin.H{
				"filepath": nil,
			})
		}else {
			var filenames []string
			for _, fp := range filepath{
				filenames = append(filenames, getFilename(fp))
			}
			c.JSON(202, gin.H{
				"filepath": filenames,
			})
		}
	})

	// Get file by filePath
	r.GET("/getFile", func(c *gin.Context) {
		operator := c.Query("operator")
		version := c.Query("version")
		filename := c.Query("filename")
		filepath := "./community-operators/"+ operator + "/" + version + "/" + filename
		file := getFilename(filepath)
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", "attachment; filename=" + file)
		c.Header("Content-Transfer-Encoding", "binary")
		c.Header("Cache-Control", "no-cache")
		http.ServeFile(c.Writer, c.Request, filepath)
	})

	r.Run(*host+":"+*port)
}

func getFilename(filepath string) string {
	parts:=SplitAny(filepath,"\\/")
	return parts[len(parts)-1]
}

func SplitAny(s string, seps string) []string {
	splitter := func(r rune) bool {
		return strings.ContainsRune(seps, r)
	}
	return strings.FieldsFunc(s, splitter)
}