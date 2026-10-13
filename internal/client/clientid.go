package client

// ClientID is the service account this client signs in as, so a resource can refuse to lock its own provider out.
func (c *Client) ClientID() string { return c.cfg.ClientID }
