This service consists out of 2 pieces: an OAuth server issuing tokens and an API Gateway that is secured by these tokens.
The service is supposed to be run together with lndhub.go, but could support multiple backends of any kind.

Deployed on regtest at `https://api.regtest.getalby.com`.
## OAuth2 server
This service is responsible for generating access tokens, so Alby users can authorize 3rd party applications
to access the Alby Wallet API in their name. Possible use-cases include:

- Allow read-only access so another app can analyze your invoices or payments, or have websocket access for settled invoices/payments.
- Allow a 3rd party app to generate invoices for your account (= eg. Lightning Address).
- Allow a 3rd party to fetch your value4value information, for example to inject it in an RSS feed.
- Allow an application to make payments automatically on your behalf, maybe with some monthly budget.

### Getting started
All examples are using [httpie](https://httpie.io)
- Make a POST request to the oauth server in order to get an access code. This should be made from the browser, as the responds redirects the client back to the client application.
	```
	http -f POST https://api.regtest.getalby.com/oauth/authorize\?client_id=test_client\&response_type=code\&redirect_uri=localhost:8080/client_app\&scope\=balance:read login=$login password=$password
	```
	- `redirect_uri` should be a web or native uri where the client should be redirected once the authorization is complete.
	- You will need a `client_id` and a `client_secret`. For regtest, you can use `test_client` and `test_secret`.
	- `response_type` should always be `code`.
	- For the possible `scope`'s, see below. These should be space-seperated (url-encoded space: `%20`).
	- `$login` and `$password` should be your LNDHub login and password.
  The response should be a `302 Found` with the `Location` header equal to the redirect URL with the code in it:
	`Location: localhost:8080/client_app?code=YOUR_CODE`
- Fetch an access token and a refresh token using the authorization code obtained in the previous step `oauth/token` by doing a HTTP POST request with form parameters:
	```
	http -a test_client:test_secret 
	-f POST https://api.regtest.getalby.com/oauth/token
	code=YOUR_CODE
	grant_type=authorization_code
	redirect_uri=localhost:8080/client_app


	HTTP/1.1 200 OK
	{
    "access_token": "your_access_token",
    "expires_in": 7200,
    "refresh_token": "your_refresh_token",
    "scope": "balance:read",
    "token_type": "Bearer"
	}
	```
	Use the client_id and the client_secret as basic authentication. Use the same redirect_uri as you used in the previous step.
### Scopes and endpoints:
WIP, more to follow
| Endpoint | Scope | Description |
|----------|-------|-------------|
| POST `/invoices`  | `invoices:create`  | Create invoices |
| GET `/invoices/incoming`  | `invoices:read`  | Read incoming payment history |
| GET `/invoices/outgoing`  | `transactions:read`  | Read outgoing payment history |
| GET `/invoices/{payment_hash}`  | `invoices:create`  | Get details about a specific invoice by payment hash |
| GET `/balance`  | `balance:read`  | Get account balance |
| GET `/user/value4value`  | `account:read`  | Read user's Lightning Address and keysend information|

## API Gateway
- Use the access token to make a request to the LNDhub API:
	```
	http https://api.regtest.getalby.com/balance Authorization:"Bearer $your_access_token"
	```

To do:
- more scopes
- websocket proxy
- budget feature

## Admin API

There is no authentication here, so the `/admin/..` route should not be accesible from outside a trusted network.

| Endpoint | Response Fields | Description |
|----------|-------|-------------|
| GET `/admin/clients`  | (array) id, imageUrl, name, url  | Get all registered clients |
| GET `/admin/clients/{clientId}`  | id, imageUrl, name, url | Get a specific client by client id|
| POST `/admin/clients`  | clientId, clientSecret, name, imageUrl, url | Create a new client|
| PUT `/admin/clients/{clientId}`  | id, name, imageUrl, url  | Update the metadata of an existing client|