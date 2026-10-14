# SPK Ocular 1.6.1

- Linux DEB/RPM 安装包的 glibc 要求从 2.39 降至 2.35 或更新版本。具备 GTK 3 和 WebKit2GTK 4.1 时，可在 Ubuntu 22.04 和 Debian 12 上正常安装。
- 两种 Linux 架构在发布前均接受 Ubuntu 22.04 原生安装、浏览器模式和 GTK 窗口验证。打包检查也会拒绝需要高于声明下限的 glibc 符号的程序。
- 原生验证压缩包在现有六个平台的证据之外，新增两份 Ubuntu 22.04 报告和截图。这些是在测试机内核上的 X11 和发行版用户空间验证，并非对所有内核、GPU 驱动或 Wayland 会话的认证。
