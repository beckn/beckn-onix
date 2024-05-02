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
  const [buttonDisable, setButtonDisable] = useState(false);
  const [networkconfigurl, setNetworkconfigurl] = useState("");

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
  const installBap = useCallback(async () => {
    const toastId = toast.loading("Installing BAP...");
    setButtonDisable(true);
    try {
      const response = await fetch("/api/install-bap", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          subscriberUrl,
          subscriberId,
          registryUrl,
          networkconfigurl,
        }),
      });

      if (response.ok) {
        console.log("BPP installed successfully");
        toast.update(toastId, {
          render: "BPP installed successfully 👌",
          type: "success",
          isLoading: false,
          autoClose: 5000,
        });
      } else {
        console.error("Failed to install BAP");
        toast.update(toastId, {
          render: "Failed to install BAP 🤯",
          type: "error",
          isLoading: false,
          autoClose: 5000,
        });
      }
    } catch (error) {
      console.error("An error occurred:", error);
      toast.update(toastId, {
        render: "Bap installation done",
        type: "success",
        isLoading: false,
        autoClose: 5000,
      });
    }
    setButtonDisable(false);
  }, [subscriberUrl, subscriberId, registryUrl, networkconfigurl]);

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
          <p className={styles.mainText}>BAP</p>
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
              label={"Network Configuration URL"}
              value={networkconfigurl}
              onChange={handleNetworkconfigurlChange}
            />

            <div className={styles.buttonsContainer}>
              <SecondaryButton text={"Cancel"} />
              <PrimaryButton
                disabled={buttonDisable}
                onClick={installBap}
                text={"Continue"}
              />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
