package main

import (
	"time"
)

//GitlabRepository represents repository information from the webhook
type GitlabRepository struct {
	Name, Url, Description, Home string
}

//Commit represents commit information from the webhook
type Commit struct {
	Id, Message, Timestamp, Url string
	Author                      Author
}

//Author represents author information from the webhook
type Author struct {
	Name, Email string
}

//Webhook represents push information from the webhook
type Webhook struct {
	Ref, User_name      string
	User_id, Project_id int
	Repository          GitlabRepository
	Commits             []Commit
	Total_commits_count int

	ctime time.Time
}

type SystemHookProjectCreate struct {
	Created_at       string
	Name, Owner_name string
}

type SystemHookProjectDestroy struct {
	Created_at          string
	Name, Owner_name    string
	Path_with_namespace string
}

type SystemHookUserCreate struct {
	Created_at, Name, Email string
}

type SystemHookGroupCreate struct {
	Created_at       string
	Name, Owner_name string
}

type SystemHookUserAddToGroup struct {
	Created_at, Group_name, User_name, User_email string
}

type SystemHookUserAddToTeam struct {
	Created_at, Project_name, User_name, User_email string
}

type SystemHookUnknown struct {
	Evt string
}
