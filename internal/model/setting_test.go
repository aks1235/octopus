package model

import "testing"

// TestSettingValidate_modelFilter 验证全局模型过滤设置的校验分支:
// 留空放行; 合法 ECMAScript 正则(含 (?i) 内联 flag)放行; 非法正则报错。
func TestSettingValidate_modelFilter(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty allowed", value: "", wantErr: false},
		{name: "plain prefix", value: "^glm", wantErr: false},
		{name: "inline flag", value: "(?i)^GLM", wantErr: false},
		{name: "lookahead", value: "^(?!.*flash).*$", wantErr: false},
		{name: "unclosed bracket", value: "^glm-[a", wantErr: true},
		{name: "unbalanced paren", value: "(glm", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setting := Setting{Key: SettingKeyModelFilter, Value: tc.value}
			err := setting.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
		})
	}
}
