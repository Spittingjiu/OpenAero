include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-openaero
PKG_VERSION:=1.0.0
PKG_RELEASE:=1
PKG_MAINTAINER:=Antigravity Team
PKG_LICENSE:=MIT

LUCI_TITLE:=LuCI support for sing-box based proxy (OpenAero)
LUCI_DEPENDENCIES:=+sing-box +kmod-tun +ca-certificates +curl +ip-full +iptables +iptables-mod-tproxy
LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
