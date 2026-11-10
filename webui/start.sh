#!/data/local/tmp/webui/busybox sh
BB=/data/local/tmp/webui/busybox
DIR=/data/local/tmp/webui
# stop any previous instance
for p in $($BB pgrep -f "httpd -p 8080" 2>/dev/null); do kill $p 2>/dev/null; done
$BB chmod 755 $DIR/busybox $DIR/www/cgi-bin/* 2>/dev/null
$BB httpd -p 8080 -h $DIR/www -c $DIR/httpd.conf
echo "httpd on :8080 (docroot $DIR/www)"
