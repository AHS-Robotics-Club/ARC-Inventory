package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

/*
This is the system ARC uses to manage our inventory. It currently
creates a quick http server and puts an html site on top of it.
The site has four fields: Barcode, Name, Team, Status. Each of the
fields is returned when the submit button is clicked for use in the writeToSheet
function. This function searches through a map of barcodes and cell numbers taken
from the google sheet. Then it appends all the values in the row number based on
what the html site returns. Finally the site refreshes for a person to enter the barcodes again.
@author Ethan Leitner (litehed)
Please contact me on github if you are haviing trouble with the program or modifying this repository.
*/

// TODO: I'd like to optimize the page and make it overall cleaner by utilizing
// websockets rather than the current method I am employing below

var BARCODES = make(map[string]string)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Read a token from the client
func getClient(config *oauth2.Config) *http.Client {
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Searches through the BARCODE map to find the cell associated with the barcode
func findCellFromTable(barcodeID string) string {
	return BARCODES[barcodeID]
}

// Checks the sheets for whether an object is available or not
func checkAvailable(srv *sheets.Service, spreadsheetId string, barcode string) string {
	readRange := "C" + findCellFromTable(barcode)
	checkedIn, _ := srv.Spreadsheets.Values.Get(spreadsheetId, readRange).Do()
	return fmt.Sprint(checkedIn.Values[0][0])
}

// Calls the checkAvailable function and then returns Checked In rather than Available
func checkStatus(srv *sheets.Service, spreadsheetId string, barcode string) string {
	checkedAsStr := checkAvailable(srv, spreadsheetId, barcode)
	if checkedAsStr == "Available" {
		return "Checked In"
	}
	return "Checked Out"
}

// Creates a map by scanning all barcodes in spreadsheet. This allows
// for easy editing of the spreadsheet while not having to change any code when doing so.
func createBarcodeList(srv *sheets.Service, spreadsheetId string) {
	writeRange := "H:H"
	checkedIn, err := srv.Spreadsheets.Values.Get(spreadsheetId, writeRange).Do()
	if err != nil {
		fmt.Println(err.Error())
		fmt.Println("Token most likely expired so just delete the current one and rerun the app to create a new one")
	}
	for index, row := range checkedIn.Values {
		barcodeStr := fmt.Sprint(row[0])
		if inequalStrings(barcodeStr) {
			BARCODES[barcodeStr] = strconv.Itoa(index + 1)
		}
	}
	fmt.Println(BARCODES)
}

//Checks for lines containing these strings and skips those lines for creation of map
func inequalStrings(barcode string) bool {
	arr := []string{
		"Barcodes:",
		"Expansion Hubs:",
		"Control Hubs:",
		"Robot Batteries:",
		"Chargers:",
		"Controllers:",
		"Phones:",
		"Drills:",
		"Motors:",
		"Servos:",
	}
	for _, s := range arr {
		if barcode == s {
			return false
		}
	}
	return true
}

// Looks for a value based on barcode table and sets it to either Available or Unavailable
func writeToSheet(srv *sheets.Service, spreadsheetId string, barcode string, name string, team string, status string) {
	writeRange := findCellFromTable(barcode)
	values := []interface{}{"Unavailable"}
	var vr sheets.ValueRange
	if status == "checkIn" {
		values = []interface{}{"Available"}
	}
	values = append(values, name, team)
	vr.Values = append(vr.Values, values)
	_, err := srv.Spreadsheets.Values.Update(spreadsheetId, "C"+writeRange, &vr).ValueInputOption("RAW").Do()
	if err != nil {
		fmt.Println("Unable to update data to sheet  ", err)
	}
}

// The html, css, and javascript of the webpage (self-explanatory)
func buildForm(results string, errmsg string, barcode string, user string, team string, status string) string {

	bc := "placeholder=\"Barcode\""
	us := "placeholder=\"Name\""
	if len(barcode) > 0 {
		bc = fmt.Sprintf("value=\"%s\"", barcode)
	}
	if len(user) > 0 {
		us = fmt.Sprintf("value=\"%s\"", user)
	}
	respstr := fmt.Sprintf(`
			<html>
			<head><title>ARC Inventory Client</title>
			<style>
			select { font-size: 24px; }
			button { font-size: 24px; }
			input[type='text'] { font-size: 24px; }
			textarea { font-size: 20px; }
			</style>
			</head>
			<body>
			<script type="text/javascript">
            	var socket = new WebSocket("ws://localhost:8080/ws")
				socket.onOpen = function() {
					console.log("Socket Connection Opened")
				}
            	socket.onclose = function(evt) {
                	console.log("Socket Connection Closed")
            	}
            	socket.onmessage = function(evt) {
   
            	}
        </script>
			<h1 style="color:blue;background-color:lightgrey;text-align:center">ARC Inventory Client</h1>
			<form action="/scan" method="post" id="samplewebresults">
   		        <fieldset>
  	    	    <legend>Scan Processor</legend>
 				<br>
		        <h2 style="color:black;">Scan into Barcode Box</h2>
				<h3 style="color:red;">Do NOT type in barcode box</h3>
				<input type="text" name="barcode" %s autocomplete="off"><br>
				<input type="text" name="user" %s autocomplete="off"><br>
				<select name="team" %s ><br>
				<option value="Crimson"> Crimson 12864</option>
				<option value="Black"> Black 9686</option>
				</select>
				<select name="status" %s ><br>
				<option value="checkOut"> Check Out</option>
				<option value="checkIn"> Check In</option>
				</select>
				<br>
				<button style="color:blue;background-color:lightgrey" type="submit" value="submitButton">  Submit  </button>
    	   	    </fieldset>
			</form>
			<br>
    		<h2 style="color:red;">%s</h2>
    		<h2 style="color:blue;">%s</h2>
 			</body>
		</html>
		`, bc, us, team, status, errmsg, results)
	return respstr
}

// Creates errors for invalid barcodes and names
func scanErrors(barcode string, name string) (int, error) {
	var err error
	errNum := -1
	x, _ := strconv.Atoi(BARCODES[barcode])
	if x < 1 {
		err = errors.New("invalid barcode: please scan again")
		errNum = 0
	}
	if len(name) < 1 {
		err = errors.New("invalid name: please re-enter name")
		errNum = 1
	}
	return errNum, err
}

func reader(conn *websocket.Conn) {
	for {
		// read in a message
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		}
		// print out that message for clarity
		log.Println(string(p))
		if err := conn.WriteMessage(messageType, p); err != nil {
			log.Println(err)
			return
		}
	}
}

func wsEndpoint(w http.ResponseWriter, r *http.Request) {
	// upgrade this connection to a WebSocket
	// connection
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
	}

	log.Println("Client Connected")
	err = ws.WriteMessage(1, []byte("Hi Client!"))
	if err != nil {
		log.Println(err)
	}
	// listen indefinitely for new messages coming
	// through on our WebSocket connection
	reader(ws)
}

// Handles reading data from the page and sending it over to the google sheet
func scanhandler(response http.ResponseWriter, request *http.Request, srv *sheets.Service, spreadsheetId string) {
	err := request.ParseForm()
	if err != nil {
		log.Fatalf("Error while parsing: %v", err)
	}

	barcode := request.PostFormValue("barcode")
	name := request.PostFormValue("user")
	team := request.PostFormValue("team")
	status := request.PostFormValue("status")

	errId, err := scanErrors(barcode, name)
	if err != nil {
		if errId == 0 {
			barcode = ""
		} else if errId == 1 {
			name = ""
		}
		fmt.Fprintf(response, buildForm("", err.Error(), barcode, name, team, checkStatus(srv, spreadsheetId, barcode)))
	} else {
		writeToSheet(srv, spreadsheetId, barcode, name, team, status)
		fmt.Fprintf(response, buildForm("", "", "", "", "", ""))
	}
}

func main() {
	ctx := context.Background()
	b, err := ioutil.ReadFile("client_secret.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// If modifying these scopes, delete previously saved token.json.
	config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/spreadsheets")
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	client := getClient(config)

	srv, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Sheets client: %v", err)
	}

	// https://docs.google.com/spreadsheets/d/1yXblmmqKpCuhtVYQ93O2lyJVEHuItLgr0dG8FqPll7g/edit
	spreadsheetId := "1yXblmmqKpCuhtVYQ93O2lyJVEHuItLgr0dG8FqPll7g"

	createBarcodeList(srv, spreadsheetId)
	listenport := "8080"

	var router = mux.NewRouter()

	router.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
		scanhandler(w, r, srv, spreadsheetId)
	})

	router.HandleFunc("/ws", wsEndpoint)

	http.Handle("/", router)

	http.ListenAndServe(":"+listenport, nil)

}
