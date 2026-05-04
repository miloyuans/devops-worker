package main

type Config struct {
	BotToken        string
	GroupChatIDs    []int64
	GroupTopicMap   map[int64]int
	ApproverUserIDs []int64
	WebAddr         string
	DataDir         string
	Timezone        string
	AdminUsername   string
	AdminPassword   string
}

type Shift struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Start     string `json:"start"`
	End       string `json:"end"`
	CrossDay  bool   `json:"cross_day"`
}

type ShiftConfig struct {
	Shifts []Shift `json:"shifts"`
}

type StaffUser struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	TelegramUserID int64  `json:"telegram_user_id"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type UserDB struct {
	Users []StaffUser `json:"users"`
}

type ScheduleRule struct {
	ID        string   `json:"id"`
	Year      int      `json:"year"`
	Month     int      `json:"month"`
	WeekNums  []int    `json:"week_nums"`
	Weekdays  []int    `json:"weekdays"`
	StaffIDs  []string `json:"staff_ids"`
	ShiftCode string   `json:"shift_code"`
	Enabled   bool     `json:"enabled"`
}

type ScheduleItem struct {
	Date           string `json:"date"`
	StaffID        string `json:"staff_id"`
	StaffName      string `json:"staff_name"`
	TelegramUserID int64  `json:"telegram_user_id"`
	ShiftCode      string `json:"shift_code"`
	ShiftName      string `json:"shift_name"`
	ShiftShortName string `json:"shift_short_name"`
	StartTime      string `json:"start_time"`
	EndTime        string `json:"end_time"`
}

type ActiveSchedule struct {
	Revision         int            `json:"revision"`
	EffectiveAt      string         `json:"effective_at"`
	ApprovedBy       int64          `json:"approved_by"`
	SourceApprovalID string         `json:"source_approval_id"`
	Items            []ScheduleItem `json:"items"`
}

type Approval struct {
	ID            string         `json:"id"`
	Status        string         `json:"status"`
	CreatedBy     string         `json:"created_by"`
	CreatedAt     string         `json:"created_at"`
	ReviewedBy    int64          `json:"reviewed_by,omitempty"`
	ReviewedAt    string         `json:"reviewed_at,omitempty"`
	ApproverIDs   []int64        `json:"approver_ids"`
	Rules         []ScheduleRule `json:"rules"`
	PreviewItems  []ScheduleItem `json:"preview_items"`
	PreviewHTML   string         `json:"preview_html"`
	BaseRevision  int            `json:"base_revision"`
	NewRevision   int            `json:"new_revision"`
	RejectReason  string         `json:"reject_reason,omitempty"`
	StatusMessage string         `json:"status_message,omitempty"`
}

type ReminderState struct {
	Sent map[string]bool `json:"sent"`
}

type PageData struct {
	Title       string
	Config      Config
	Active      ActiveSchedule
	TodayItems  []ScheduleItem
	Users       []StaffUser
	Shifts      []Shift
	Approvals   []Approval
	HistoryDate string
	History     []ScheduleItem
	Message     string
	Error       string
	NowYear     int
	NowMonth    int
	NowDate     string
	Months      []int
	WeekNums    []int
}
