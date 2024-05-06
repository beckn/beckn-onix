"use client";

import InputField from "@/components/InputField/InputField";
import styles from "../../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import SecondaryButton from "@/components/Buttons/SecondaryButton";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import { useState, useCallback } from "react";
import { toast } from "react-toastify";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function Home() {
  const [subscriberUrl, setSubscriberUrl] = useState("");
  const [subscriberId, setSubscriberId] = useState("");
  const [registryUrl, setRegistryUrl] = useState("");
  const [networkconfigurl, setNetworkconfigurl] = useState("");
  const [webhookUrl, setWebhookUrl] = useState("");
  const [buttonDisable, setButtonDisable] = useState(false);
  const handleSubscriberUrlChange = (event) => {
    setSubscriberUrl(event.target.value);
  };

  const handleSubscriberIdChange = (event) => {
    setSubscriberId(event.target.value);
  };

  const handleRegistryUrlChange = (event) => {
    setRegistryUrl(event.target.value);
  };
  const handleNetworkconfigurlChange = (event) => {
    setNetworkconfigurl(event.target.value);
  };
  const handleWebhookUrlChange = (event) => {
    setWebhookUrl(event.target.value);
  };

  const installBpp = useCallback(async () => {
    const toastId = toast.loading("Installing BPP...");
    setButtonDisable(true);
    try {
      const response = await toast.promise(
        fetch("/api/install-bpp", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            subscriberUrl: subscriberUrl,
            subscriberId: subscriberId,
            registryUrl: registryUrl,
            networkconfigurl: networkconfigurl,
            webhookUrl: webhookUrl,
          }),
        }),
        {
          success: "BPP installed successfully 👌",
          error: "Failed to install BPP 🤯",
        }
      );

      if (response.ok) {
        console.log("BPP installed successfully");
        toast.update(toastId, {
          render: "BPP installed successfully 👌",
          type: "success",
          isLoading: false,
          autoClose: 5000,
        });
      } else {
        console.error("Failed to install BPP");
        toast.update(toastId, {
          render: "Failed to install BPP 🤯",
          type: "error",
          isLoading: false,
          autoClose: 5000,
        });
      }
    } catch (error) {
      console.error("An error occurred:", error);
      toast.update(toastId, {
        render: "An error occurred while installing BPP 😥",
        type: "error",
        isLoading: false,
        autoClose: 5000,
      });
    }
    setButtonDisable(false);
  }, [subscriberUrl, subscriberId, registryUrl, networkconfigurl, webhookUrl]);
  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <button
            onClick={() => window.history.back()}
            className={styles.backButton}
          >
            Back
          </button>
          <p className={styles.mainText}>BPP</p>
          <div className={styles.formContainer}>
            <InputField
              label={"Subscriber ID"}
              value={subscriberId}
              onChange={handleSubscriberIdChange}
            />
            <InputField
              label={"Subscriber URL"}
              value={subscriberUrl}
              onChange={handleSubscriberUrlChange}
            />

            <InputField
              label={"Registry URL"}
              value={registryUrl}
              onChange={handleRegistryUrlChange}
            />
            <InputField
              label={"Webhook URL"}
              value={webhookUrl}
              onChange={handleWebhookUrlChange}
            />
            <InputField
              label={"Network Configuration URL"}
              value={networkconfigurl}
              onChange={handleNetworkconfigurlChange}
            />

            <div className={styles.buttonsContainer}>
              {/* <SecondaryButton text={"Cancel"} /> */}
              <PrimaryButton
                disable={buttonDisable}
                onClick={installBpp}
                text={"Continue"}
              />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
