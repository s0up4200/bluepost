pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "io.github.s0up4200.bluepost"
  ipcTarget: "io.github.s0up4200.bluepost"
  manageIpc: false

  property var status: ({state: "stopped", map: false, pbap: false, storage: "locked"})
  property var messages: []
  property string feedback: ""
  property bool streamReady: false
  property bool cursorActive: false
  property int cursorIndex: 0

  readonly property string bluepostBinary: Quickshell.env("HOME") + "/.local/bin/bluepost"
  readonly property bool connected: streamReady && status.state === "ready" && status.map === true
  readonly property string summary: !streamReady ? "Daemon stopped"
    : connected ? "Connected" : "Reconnecting"
  readonly property int actionCount: messages.length + (connected ? 0 : 1)
  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  function acceptLine(line) {
    try {
      var snapshot = JSON.parse(line)
      if (!snapshot || !snapshot.status || typeof snapshot.status !== "object"
          || !(snapshot.messages instanceof Array)) return
      status = snapshot.status
      messages = snapshot.messages.slice(0, 5)
      streamReady = true
      feedback = ""
    } catch (error) {
      // Ignore partial or malformed lines and keep the last valid snapshot.
    }
  }

  function copyMessage(message) {
    var text = String(message.copy_text || "")
    if (text === "") return
    clipboard.payload = text
    clipboard.kind = String(message.copy_kind || "message")
    clipboard.stdinEnabled = true
    clipboard.exec(["/usr/bin/wl-copy", "--sensitive", "--trim-newline"])
  }

  function reconnect() {
    feedback = "Reconnecting"
    Quickshell.execDetached(["/usr/bin/systemctl", "--user", "restart", "bluepost.service"])
  }

  function moveCursor(delta) {
    if (actionCount === 0) return
    if (!cursorActive) {
      cursorActive = true
      cursorIndex = 0
    } else {
      cursorIndex = Math.max(0, Math.min(actionCount - 1, cursorIndex + delta))
    }
    scrollCursorIntoView()
  }

  function activateCursor() {
    if (!cursorActive || actionCount === 0) return
    if (cursorIndex < messages.length) copyMessage(messages[cursorIndex])
    else reconnect()
  }

  function scrollCursorIntoView() {
    Qt.callLater(function() {
      var item = root.cursorIndex < root.messages.length
        ? messageRepeater.itemAt(root.cursorIndex) : reconnectRow
      if (!item || !panelFlick) return
      var point = item.mapToItem(panelFlick.contentItem, 0, 0)
      var top = point.y
      var bottom = top + item.height
      if (top < panelFlick.contentY) panelFlick.contentY = top
      else if (bottom > panelFlick.contentY + panelFlick.height)
        panelFlick.contentY = Math.min(panelFlick.contentHeight - panelFlick.height,
          bottom - panelFlick.height)
    })
  }

  function storageLabel() {
    var value = String(status.storage || "locked")
    return value.charAt(0).toUpperCase() + value.slice(1)
  }

  onOpenedChanged: if (opened) {
    cursorActive = false
    panelFlick.contentY = 0
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }
  onActionCountChanged: cursorIndex = Math.max(0, Math.min(cursorIndex, actionCount - 1))

  Process {
    id: widgetStream
    running: true
    command: [root.bluepostBinary, "widget"]
    stdout: SplitParser {
      onRead: function(line) { root.acceptLine(line) }
    }
    onExited: {
      root.streamReady = false
      root.status = ({state: "stopped", map: false, pbap: false, storage: "locked"})
      streamRestart.restart()
    }
  }

  Timer {
    id: streamRestart
    interval: 5000
    repeat: false
    onTriggered: widgetStream.running = true
  }

  Process {
    id: clipboard
    property string payload: ""
    property string kind: "message"
    stdinEnabled: true
    onStarted: {
      write(payload)
      payload = ""
      stdinEnabled = false
    }
    onExited: function(exitCode) {
      root.feedback = exitCode === 0
        ? (kind === "code" ? "Code copied" : "Message copied")
        : "Copy failed"
      feedbackTimer.restart()
    }
  }

  Timer {
    id: feedbackTimer
    interval: 2500
    repeat: false
    onTriggered: root.feedback = ""
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function status(): string { return root.summary }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "󰄜"
    foreground: root.connected ? root.barForeground : Qt.darker(root.barForeground, 1.55)
    tooltipText: "Bluepost: " + root.summary
    onPressed: root.toggle()
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(380))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(620))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onMoveRequested: function(dx, dy) { if (dy !== 0) root.moveCursor(dy) }
      onActivateRequested: root.activateCursor()
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }

      Flickable {
        id: panelFlick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        flickableDirection: Flickable.VerticalFlick
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: panelFlick.width
          spacing: Style.space(12)

          PanelHero {
            width: parent.width
            title: "iPhone"
            meta: root.summary
            foreground: root.foreground
            fontFamily: root.fontFamily
            iconOpacity: root.connected ? 1.0 : 0.5
            iconComponent: Component {
              Text {
                text: "󰄜"
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.display
              }
            }
          }

          Column {
            width: parent.width
            spacing: Style.spacing.labelGap
            InfoPair { label: "Messages"; value: root.status.map === true ? "Connected" : "Unavailable" }
            InfoPair { label: "Contacts"; value: root.status.pbap === true ? "Connected" : "Unavailable" }
            InfoPair { label: "Storage"; value: root.storageLabel() }
          }

          Text {
            visible: root.feedback !== ""
            width: parent.width
            text: root.feedback
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          PanelSeparator {
            foreground: root.foreground
          }

          Column {
            width: parent.width
            spacing: Style.space(8)

            PanelSectionHeader {
              text: "RECENT SMS"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }

            Text {
              visible: root.messages.length === 0
              width: parent.width
              text: "No messages yet"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              horizontalAlignment: Text.AlignHCenter
            }

            Column {
              id: messageColumn
              visible: root.messages.length > 0
              width: parent.width
              spacing: Style.space(4)

              Repeater {
                id: messageRepeater
                model: root.messages
                MessageRow {
                  required property var modelData
                  required property int index
                  width: messageColumn.width
                  message: modelData
                  rowIndex: index
                }
              }
            }

            ReconnectRow {
              id: reconnectRow
              visible: !root.connected
              width: parent.width
              rowIndex: root.messages.length
            }
          }
        }
      }
    }
  }

  component InfoPair: RowLayout {
    property string label: ""
    property string value: ""
    width: parent.width
    spacing: Style.space(8)

    Text {
      text: parent.label
      color: root.dim
      font.family: root.fontFamily
      font.pixelSize: Style.font.body
    }
    Item { Layout.fillWidth: true }
    Text {
      text: parent.value
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.body
      font.bold: true
    }
  }

  component MessageRow: CursorSurface {
    id: row
    required property var message
    required property int rowIndex
    hasCursor: root.cursorActive && root.cursorIndex === rowIndex
    foreground: root.foreground
    implicitHeight: messageContent.implicitHeight + Style.space(16)

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onEntered: {
        root.cursorActive = true
        root.cursorIndex = row.rowIndex
      }
      onClicked: root.copyMessage(row.message)
    }

    ColumnLayout {
      id: messageContent
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.verticalCenter: parent.verticalCenter
      anchors.leftMargin: Style.space(10)
      anchors.rightMargin: Style.space(10)
      spacing: Style.space(2)

      RowLayout {
        Layout.fillWidth: true
        spacing: Style.space(8)
        Text {
          Layout.fillWidth: true
          text: String(row.message.sender || "Unknown sender")
          color: root.foreground
          font.family: root.fontFamily
          font.pixelSize: Style.font.body
          font.bold: true
          elide: Text.ElideRight
        }
        Text {
          visible: row.message.copy_kind === "code"
          text: "CODE"
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          font.bold: true
        }
      }

      Text {
        Layout.fillWidth: true
        text: String(row.message.body || "")
        textFormat: Text.PlainText
        color: root.dim
        font.family: root.fontFamily
        font.pixelSize: Style.font.bodySmall
        wrapMode: Text.Wrap
        maximumLineCount: 2
        elide: Text.ElideRight
      }
    }
  }

  component ReconnectRow: CursorSurface {
    id: row
    required property int rowIndex
    hasCursor: root.cursorActive && root.cursorIndex === rowIndex
    foreground: root.foreground
    implicitHeight: reconnectText.implicitHeight + Style.space(18)

    MouseArea {
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onEntered: {
        root.cursorActive = true
        root.cursorIndex = row.rowIndex
      }
      onClicked: root.reconnect()
    }

    Text {
      id: reconnectText
      anchors.centerIn: parent
      text: "Reconnect now"
      color: root.foreground
      font.family: root.fontFamily
      font.pixelSize: Style.font.body
      font.bold: true
    }
  }
}
