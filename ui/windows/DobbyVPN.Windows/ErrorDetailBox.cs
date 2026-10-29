using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace DobbyVPN.Windows;

public sealed class ErrorDetailBox : TextBox
{
    protected override void OnApplyTemplate()
    {
        base.OnApplyTemplate();
        if (GetTemplateChild("ContentElement") is ScrollViewer viewport)
        {
            AutomationProperties.SetAutomationId(viewport, "error.detail.viewport");
            AutomationProperties.SetName(viewport, "Error details viewport");
            AutomationProperties.SetAccessibilityView(viewport, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Control);
        }
    }
}
