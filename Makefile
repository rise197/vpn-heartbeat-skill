.PHONY: build run clean install-skill

# 构建 Go 二进制
build:
	go build -o vpn-skill ./cmd/skill/

# 运行（前台模式，输出 JSON 状态）
run: build
	./vpn-skill -config config.yaml

# 运行（后台模式，适合 AI 加载时启动）
daemon: build
	./vpn-skill -config config.yaml &
	echo $$! > .pid

# 停止后台进程
stop:
	@test -f .pid && kill $$(cat .pid) 2>/dev/null || true
	@rm -f .pid

# 安装到 Claude Code（注册 SessionStart hook）
install-skill:
	@echo "=== 安装 VPN Heartbeat Skill ==="
	@echo ""
	@echo "将以下内容添加到 ~/.claude/settings.json 的 hooks 字段:"
	@echo ""
	@echo '  "SessionStart": [{'
	@echo '    "matcher": "",'
	@echo '    "hooks": [{'
	@echo '      "type": "command",'
	@echo '      "command": "cd $(PWD) && make daemon",'
	@echo '      "timeout": 30,'
	@echo '      "statusMessage": "CCDC VPN 心跳认证中..."'
	@echo '    }]'
	@echo '  }]'
	@echo ""
	@echo "Skill 已就绪: $(PWD)"

# 清理
clean:
	rm -f vpn-skill .pid
