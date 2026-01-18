package misc

type FriendGroupItem struct {
	Name      string  `json:"name"`
	Avatar    *string `json:"avatar"`
	Bg        *string `json:"bg"`
	GroupInfo string  `json:"groupInfo"`
	Detail    string  `json:"detail"`
	Url       *string `json:"url,omitempty"`
}

type FriendGroupData struct {
	Group     string            `json:"group"`
	GroupList []FriendGroupItem `json:"groupList"`
}
