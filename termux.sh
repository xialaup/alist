#!/data/data/com.termux/files/usr/bin/sh
termux-change-repo
pkg update
echo '安装依赖'
pkg instal termux-services tsu vim -y
apt install -y wget dpkg
echo '安装Alist'
wget https://ghfast.top/https://github.com/ykxVK8yL5L/termux-packages/releases/v1/download/alist_1_aarch64.deb -O alist.deb
dpkg -i alist.deb
alist admin set admin
echo '创建开机启动服务'
mkdir -p $PREFIX/var/service/alist/log 
ln -sf $PREFIX/share/termux-services/svlogger $PREFIX/var/service/alist/log/run
echo '#!/data/data/com.termux/files/usr/bin/sh' > $PREFIX/var/service/alist/run
echo 'exec 2>&1' >> $PREFIX/var/service/alist/run
echo 'cd ~ && alist server' >> $PREFIX/var/service/alist/run
chmod a+x $PREFIX/var/service/alist/run
sv-enable alist 
sv up alist 
