package model

// GroupChannelOrder 是正则分组内一条人工渠道顺序偏好。
// 顺序是分组私有的: 同一渠道在不同分组可排不同位置, 由此顺序表挂在 (分组, 渠道) 二元组上而非渠道实体;
// 它只影响重算时的成员排列(渠道块之间的先后), 不改变正则定稿的成员集合。
// 渠道删除时对应行由外键级联清理; 渠道以新主键重新添加后没有顺序记录, 自然回落到自然序段。
type GroupChannelOrder struct {
	ID        int      `json:"id" gorm:"primaryKey"`                                                             // 顺序行主键。
	GroupID   int      `json:"group_id" gorm:"not null;index:idx_group_channel,unique;index:idx_group_position"` // 所属分组 ID, 与渠道共同唯一; 与 Position 组成读取路径的 (分组, 序号) 复合索引。
	Group     *Group   `json:"-" gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`                          // 仅用于声明级联外键: 分组删除时顺序随之删除。
	ChannelID int      `json:"channel_id" gorm:"not null;index:idx_group_channel,unique"`                        // 顺序指向的渠道 ID。
	Channel   *Channel `json:"-" gorm:"foreignKey:ChannelID;constraint:OnDelete:CASCADE"`                        // 仅用于声明级联外键: 渠道删除时顺序随之删除。
	Position  int      `json:"position" gorm:"not null;index:idx_group_position"`                                // 该渠道在分组内的人工序号, 从 1 起。
}
