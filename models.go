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
	Timezone  string `json:"timezone"`
	CrossDay  bool   `json:"cross_day"`
	Enabled   bool   `json:"enabled"`
	CreatedBy string `json:"created_by,omitempty"` // system/admin/user，用于 Web 权限隔离
}

type ShiftConfig struct {
	Shifts []Shift `json:"shifts"`
}

type StaffUser struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	TelegramUserID int64  `json:"telegram_user_id"`
	Enabled        bool   `json:"enabled"`
	CreatedBy      string `json:"created_by,omitempty"` // admin/user，用于 Web 权限隔离
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type UserDB struct {
	Users []StaffUser `json:"users"`
}

type ScheduleDraftChange struct {
	Dates     []string `json:"dates"`
	StaffIDs  []string `json:"staff_ids"`
	ShiftCode string   `json:"shift_code"`
}

type ScheduleRule struct {
	ID        string   `json:"id"`
	Year      int      `json:"year"`
	Month     int      `json:"month"`
	Dates     []string `json:"dates,omitempty"`
	WeekNums  []int    `json:"week_nums,omitempty"`
	Weekdays  []int    `json:"weekdays,omitempty"`
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

type ScheduleItemStatus struct {
	ScheduleItem
	StartClock        string `json:"start_clock"`
	EndClock          string `json:"end_clock"`
	NotifyStatus      string `json:"notify_status"`
	NotifyStatusLabel string `json:"notify_status_label"`
	ReadStatus        string `json:"read_status"`
	ReadStatusLabel   string `json:"read_status_label"`
}

type ActiveSchedule struct {
	Revision         int            `json:"revision"`
	VersionID        string         `json:"version_id"`
	EffectiveAt      string         `json:"effective_at"`
	ApprovedBy       int64          `json:"approved_by"`
	SourceApprovalID string         `json:"source_approval_id"`
	Items            []ScheduleItem `json:"items"`
}

type ApprovalMessageRef struct {
	ChatID    int64 `json:"chat_id"`
	ThreadID  int   `json:"thread_id,omitempty"`
	MessageID int   `json:"message_id"`
}

type Approval struct {
	ID            string               `json:"id"`
	TransactionID string               `json:"transaction_id"`
	Status        string               `json:"status"`
	CreatedBy     string               `json:"created_by"`
	CreatedAt     string               `json:"created_at"`
	ReviewedBy    int64                `json:"reviewed_by,omitempty"`
	ReviewedAt    string               `json:"reviewed_at,omitempty"`
	ApproverIDs   []int64              `json:"approver_ids"`
	Rules         []ScheduleRule       `json:"rules"`
	PreviewItems  []ScheduleItem       `json:"preview_items"`
	PreviewHTML   string               `json:"preview_html"`
	MessageRefs   []ApprovalMessageRef `json:"message_refs,omitempty"`
	BaseRevision  int                  `json:"base_revision"`
	NewRevision   int                  `json:"new_revision"`
	RejectReason  string               `json:"reject_reason,omitempty"`
	StatusMessage string               `json:"status_message,omitempty"`
}

type NotificationRecord struct {
	ID             string `json:"id"`
	Key            string `json:"key"`
	ItemKey        string `json:"item_key"`
	Date           string `json:"date"`
	StaffID        string `json:"staff_id"`
	StaffName      string `json:"staff_name"`
	ShiftCode      string `json:"shift_code"`
	ShiftName      string `json:"shift_name"`
	TelegramUserID int64  `json:"telegram_user_id"`
	ChatID         int64  `json:"chat_id"`
	SentAt         string `json:"sent_at"`
	ReadAt         string `json:"read_at,omitempty"`
	ReadBy         int64  `json:"read_by,omitempty"`
}

type NotificationState struct {
	Records []NotificationRecord `json:"records"`
}

type ReminderState struct {
	Sent map[string]bool `json:"sent"`
}

type CalendarDay struct {
	Date           string
	Day            int
	IsCurrentMonth bool
	IsToday        bool
	IsSelected     bool
	IsWeekend      bool
	HolidayName    string
	HolidayType    string
	Items          []ScheduleItem
}

type TimezoneOption struct {
	Name  string
	Label string
}

type ShiftUpdateSummary struct {
	ChangedItems int
	NewRevision  int
	VersionID    string
}

type PageData struct {
	Title             string
	Role              string
	IsAdmin           bool
	Config            Config
	Active            ActiveSchedule
	Users             []StaffUser
	Shifts            []Shift
	Approvals         []Approval
	HistoryDate       string
	History           []ScheduleItemStatus
	Message           string
	Error             string
	NowYear           int
	NowMonth          int
	NowDate           string
	Months            []int
	WeekNums          []int
	TimeOptions       []string
	TimezoneOptions   []TimezoneOption
	CalendarYear      int
	CalendarMonth     int
	CalendarDays      []CalendarDay
	SelectedDate      string
	SelectedDayItems  []ScheduleItemStatus
	DayStatus         map[string][]ScheduleItemStatus
	CalendarPrevYear  int
	CalendarPrevMonth int
	CalendarNextYear  int
	CalendarNextMonth int
}
